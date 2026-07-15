//go:build linux

package codex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	_ "modernc.org/sqlite"
)

func TestParseCodexSessionFile_NonMatchingMetadataReturnsBeforeTranscriptEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	path := fmt.Sprintf("/proc/self/fd/%d", reader.Fd())
	done := make(chan bool, 1)
	go func() {
		done <- parseCodexSessionFile(path, "/wanted/worktree") == nil
	}()

	if _, err := writer.WriteString(`{"type":"session_meta","payload":{"id":"other-session","cwd":"/other/worktree"}}` + "\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case filtered := <-done:
		if !filtered {
			t.Fatal("expected non-matching session to be filtered")
		}
	case <-time.After(5 * time.Second):
		_ = writer.Close()
		<-done
		t.Fatal("non-matching session waited for transcript EOF instead of returning after session_meta")
	}
}

func TestListCodexSessions_UsesStateDBWithoutScanningTranscripts(t *testing.T) {
	codexHome := t.TempDir()
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := os.Symlink(fmt.Sprintf("/proc/self/fd/%d", reader.Fd()), filepath.Join(sessionsDir, "blocking.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(`{"type":"session_meta","payload":{"id":"blocking","cwd":"/other/worktree"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	indexedPath := filepath.Join(sessionsDir, "indexed.jsonl")
	if err := os.WriteFile(indexedPath, []byte(`{"type":"session_meta","payload":{"id":"state-session","cwd":"/wanted/worktree","source":"exec","originator":"codex_exec"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(sessionsDir, "missing.jsonl")
	missingTranscript := `{"type":"session_meta","payload":{"id":"transcript-only","cwd":"/wanted/worktree","source":"exec","originator":"codex_exec"}}` + "\n" +
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"transcript-only prompt"}]}}` + "\n" +
		`{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"answer"}]}}` + "\n"
	if err := os.WriteFile(missingPath, []byte(missingTranscript), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (
		id TEXT, rollout_path TEXT, updated_at INTEGER, updated_at_ms INTEGER,
		cwd TEXT, title TEXT, first_user_message TEXT, preview TEXT,
		archived INTEGER, has_user_event INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"state-session", "/tmp/state-session.jsonl", 1_700_000_000, nil,
		"/wanted/worktree", strings.Repeat("x", 61)+"\nextra line", "", "", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	type result struct {
		sessions []core.AgentSessionInfo
		err      error
	}
	done := make(chan result, 1)
	go func() {
		sessions, err := listCodexSessions("/wanted/worktree", codexHome)
		done <- result{sessions: sessions, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.sessions) != 2 {
			t.Fatalf("unexpected indexed sessions: %#v", got.sessions)
		}
		byID := make(map[string]core.AgentSessionInfo, len(got.sessions))
		for _, session := range got.sessions {
			byID[session.ID] = session
		}
		if byID["state-session"].Summary != strings.Repeat("x", 60)+"..." {
			t.Fatalf("state DB summary was not normalized: %q", byID["state-session"].Summary)
		}
		if byID["state-session"].MessageCount != -1 {
			t.Fatalf("state DB message count = %d, want unknown (-1)", byID["state-session"].MessageCount)
		}
		if want := time.Unix(1_700_000_000, 0); !byID["state-session"].ModifiedAt.Equal(want) {
			t.Fatalf("state DB modified time = %s, want %s", byID["state-session"].ModifiedAt, want)
		}
		missing, ok := byID["transcript-only"]
		if !ok {
			t.Fatalf("transcript-only session was omitted: %#v", got.sessions)
		}
		if missing.Summary != "transcript-only prompt" || missing.MessageCount != 2 {
			t.Fatalf("transcript-only metadata was not restored: %#v", missing)
		}
		patched, err := os.ReadFile(indexedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(patched), `"source":"cli"`) || !strings.Contains(string(patched), `"originator":"codex_cli_rs"`) {
			t.Fatalf("indexed transcript source was not patched: %s", patched)
		}
	case <-time.After(5 * time.Second):
		_ = writer.Close()
		<-done
		t.Fatal("session listing scanned transcript files instead of using the Codex state DB")
	}
}
