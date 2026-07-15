package codex

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
	_ "modernc.org/sqlite"
)

// resolveCodexHomeDir returns the effective CODEX_HOME directory.
// Priority: explicit config value > CODEX_HOME env > ~/.codex
func resolveCodexHomeDir(explicit string) string {
	if h := strings.TrimSpace(explicit); h != "" {
		return h
	}
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".codex")
}

// listCodexSessions scans the codex sessions directory for JSONL transcript
// files whose cwd matches workDir.
func listCodexSessions(workDir, codexHome string) ([]core.AgentSessionInfo, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}

	resolvedHome := resolveCodexHomeDir(codexHome)
	if sessions, ok := listCodexSessionsFromStateDB(resolvedHome, absWorkDir); ok {
		return mergeCodexSessionMetadata(sessions, filepath.Join(resolvedHome, "sessions"), absWorkDir), nil
	}

	sessionsDir := filepath.Join(resolvedHome, "sessions")

	var files []string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		return nil, nil
	}

	var sessions []core.AgentSessionInfo
	for _, f := range files {
		info := parseCodexSessionFile(f, absWorkDir)
		if info != nil {
			patchSessionSourceFile(f)
			sessions = append(sessions, *info)
		}
	}

	sortCodexSessions(sessions)

	return sessions, nil
}

func listCodexSessionsFromStateDB(codexHome, workDir string) ([]core.AgentSessionInfo, bool) {
	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, false
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, false
	}
	defer db.Close()

	columns, err := codexThreadColumns(db)
	if err != nil || !columns["id"] || !columns["cwd"] {
		return nil, false
	}

	summaryParts := make([]string, 0, 3)
	for _, column := range []string{"title", "first_user_message", "preview"} {
		if columns[column] {
			summaryParts = append(summaryParts, "NULLIF("+column+", '')")
		}
	}
	summaryExpr := "''"
	if len(summaryParts) > 0 {
		summaryExpr = "COALESCE(" + strings.Join(summaryParts, ", ") + ", '')"
	}

	updatedParts := make([]string, 0, 2)
	if columns["updated_at_ms"] {
		updatedParts = append(updatedParts, "updated_at_ms")
	}
	if columns["updated_at"] {
		updatedParts = append(updatedParts, "updated_at * 1000")
	}
	updatedExpr := "0"
	if len(updatedParts) > 0 {
		updatedExpr = "COALESCE(" + strings.Join(updatedParts, ", ") + ", 0)"
	}

	where := "cwd = ?"
	if columns["archived"] {
		where += " AND archived = 0"
	}
	query := "SELECT id, " + summaryExpr + ", " + updatedExpr +
		" FROM threads WHERE " + where + " ORDER BY " + updatedExpr + " DESC, id DESC"
	rows, err := db.Query(query, workDir)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	var sessions []core.AgentSessionInfo
	for rows.Next() {
		var id, summary string
		var updatedAtMS int64
		if err := rows.Scan(&id, &summary, &updatedAtMS); err != nil {
			return nil, false
		}
		sessions = append(sessions, core.AgentSessionInfo{
			ID:           id,
			Summary:      normalizeCodexSessionSummary(summary),
			MessageCount: -1,
			ModifiedAt:   time.UnixMilli(updatedAtMS),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false
	}
	return sessions, true
}

func mergeCodexSessionMetadata(indexed []core.AgentSessionInfo, sessionsDir, workDir string) []core.AgentSessionInfo {
	known := make(map[string]struct{}, len(indexed))
	for _, session := range indexed {
		known[session.ID] = struct{}{}
	}

	sessions := append([]core.AgentSessionInfo(nil), indexed...)
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		id, cwd, needsSourcePatch, ok := readCodexSessionMetadata(path)
		if !ok || cwd != workDir {
			return nil
		}
		if _, exists := known[id]; exists {
			if needsSourcePatch {
				patchSessionSourceFile(path)
			}
			return nil
		}
		sessionInfo := parseCodexSessionFile(path, workDir)
		if sessionInfo == nil {
			return nil
		}
		if needsSourcePatch {
			patchSessionSourceFile(path)
		}
		known[id] = struct{}{}
		sessions = append(sessions, *sessionInfo)
		return nil
	})

	sortCodexSessions(sessions)
	return sessions
}

func readCodexSessionMetadata(path string) (id, cwd string, needsSourcePatch, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Type != "session_meta" || entry.Payload.ID == "" {
			return "", "", false, false
		}
		return entry.Payload.ID, entry.Payload.Cwd, bytes.Contains(scanner.Bytes(), []byte(`"source":"exec"`)), true
	}
	return "", "", false, false
}

func sortCodexSessions(sessions []core.AgentSessionInfo) {
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].ModifiedAt.Equal(sessions[j].ModifiedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})
}

func normalizeCodexSessionSummary(summary string) string {
	summary = strings.Join(strings.Fields(summary), " ")
	if len([]rune(summary)) > 60 {
		return string([]rune(summary)[:60]) + "..."
	}
	return summary
}

func codexThreadColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(threads)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// parseCodexSessionFile reads a Codex JSONL transcript.
// Returns nil if the session's cwd doesn't match filterCwd.
func parseCodexSessionFile(path, filterCwd string) *core.AgentSessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil
	}

	var sessionID string
	var sessionCwd string
	var summary string
	var msgCount int
	userMsgSeen := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "session_meta":
			var meta struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(entry.Payload, &meta) == nil {
				sessionID = meta.ID
				sessionCwd = meta.Cwd
				if filterCwd != "" && sessionCwd != "" && sessionCwd != filterCwd {
					return nil
				}
			}

		case "response_item":
			var item struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(entry.Payload, &item) == nil {
				if item.Role == "user" {
					userMsgSeen++
					msgCount++
					// The actual user prompt is the last user response_item
					// (earlier ones are system/AGENTS.md instructions).
					// Pick the last content block that looks like a real prompt.
					for _, c := range item.Content {
						if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
							summary = c.Text
						}
					}
				} else if item.Role == "assistant" {
					msgCount++
				}
			}
		}
	}

	// Filter by cwd
	if filterCwd != "" && sessionCwd != "" && sessionCwd != filterCwd {
		return nil
	}

	if sessionID == "" {
		return nil
	}

	summary = normalizeCodexSessionSummary(summary)

	return &core.AgentSessionInfo{
		ID:           sessionID,
		Summary:      summary,
		MessageCount: msgCount,
		ModifiedAt:   stat.ModTime(),
	}
}

// findSessionFile locates the JSONL transcript for a given session ID.
func findSessionFile(sessionID, codexHome string) string {
	sessionsDir := filepath.Join(resolveCodexHomeDir(codexHome), "sessions")

	var found string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return nil
		}
		if strings.Contains(filepath.Base(path), sessionID) {
			found = path
		}
		return nil
	})
	return found
}

// getSessionHistory reads the JSONL transcript and returns user/assistant messages.
func getSessionHistory(sessionID, codexHome string, limit int) ([]core.HistoryEntry, error) {
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return nil, fmt.Errorf("session file not found for %s", sessionID)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []core.HistoryEntry

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		if raw.Type != "response_item" {
			continue
		}

		var item struct {
			Role    string `json:"role"`
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Payload, &item) != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		switch {
		case item.Role == "user" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
					entries = append(entries, core.HistoryEntry{
						Role: "user", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Role == "assistant" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					entries = append(entries, core.HistoryEntry{
						Role: "assistant", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Type == "reasoning" && item.Text != "":
			// skip reasoning items
		}
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// patchSessionSource rewrites the session_meta line in a Codex JSONL transcript
// so that source="cli" and originator="codex_cli_rs", making the session visible
// in the interactive `codex` terminal.
func patchSessionSource(sessionID, codexHome string) {
	path := findSessionFile(sessionID, codexHome)
	if path == "" {
		return
	}
	patchSessionSourceFile(path)
}

func patchSessionSourceFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	needsPatch := scanner.Scan() && bytes.Contains(scanner.Bytes(), []byte(`"source":"exec"`))
	_ = f.Close()
	if !needsPatch {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return
	}
	firstLine := data[:idx]

	// Only patch if it's actually an exec-sourced session
	if !bytes.Contains(firstLine, []byte(`"source":"exec"`)) {
		return
	}

	patched := bytes.Replace(firstLine, []byte(`"source":"exec"`), []byte(`"source":"cli"`), 1)
	patched = bytes.Replace(patched, []byte(`"originator":"codex_exec"`), []byte(`"originator":"codex_cli_rs"`), 1)

	if bytes.Equal(patched, firstLine) {
		return
	}

	out := make([]byte, 0, len(patched)+len(data)-idx)
	out = append(out, patched...)
	out = append(out, data[idx:]...)

	_ = os.WriteFile(path, out, 0o644)
}

// isUserPrompt returns true if the text looks like an actual user prompt
// rather than system context (AGENTS.md, environment_context, permissions, etc.)
func isUserPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	// Skip XML-style system context
	if strings.HasPrefix(t, "<") {
		return false
	}
	// Skip AGENTS.md instructions injected by Codex
	if strings.HasPrefix(t, "# AGENTS.md") || strings.HasPrefix(t, "#AGENTS.md") {
		return false
	}
	return true
}
