# Codex Role Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 cc-connect 的 Codex 项目增加 `/role`，从本机 agent TOML 选择角色并让新会话复用该角色的模型、推理强度和开发指令。

**Architecture:** `agent/codex` 解析并应用 profile；`core` 通过可选接口提供命令和卡片；`config` 仅保存选择的 profile 名称，原始 profile 文件仍是真源。

**Tech Stack:** Go、BurntSushi TOML、现有 core command/card/session 系统。

---

### Task 1: Parse and apply Codex agent profiles

**Files:**
- Create: `agent/codex/role.go`
- Create: `agent/codex/role_test.go`
- Modify: `agent/codex/codex.go`
- Modify: `core/interfaces.go`

- [ ] **Step 1: Write failing parser and application tests**

```go
func TestLoadRoleProfiles_UsesOnlyRunnableProfiles(t *testing.T) {
    dir := t.TempDir()
    writeProfile(t, dir, "reviewer.toml", `name = "reviewer"
description = "Independent review"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"
developer_instructions = "Review only."`)
    writeProfile(t, dir, "invalid.toml", `name = "invalid"`)

    got, err := loadRoleProfiles(dir)
    if err != nil { t.Fatal(err) }
    if len(got) != 1 || got[0].Name != "reviewer" { t.Fatalf("roles = %#v", got) }
}

func TestAgentSetRole_AppliesProfileWithoutChangingBaseMode(t *testing.T) {
    a := newRoleTestAgent(t, "yolo", []core.AgentRole{{Name: "reviewer", Model: "gpt-5.6-sol", ReasoningEffort: "high", DeveloperInstructions: "Review only."}})
    if err := a.SetRole("reviewer"); err != nil { t.Fatal(err) }
    if a.GetModel() != "gpt-5.6-sol" || a.GetReasoningEffort() != "high" || a.GetMode() != "yolo" { t.Fatal("profile runtime fields were not applied") }
}
```

- [ ] **Step 2: Run the tests and confirm failure**

Run: `go test ./agent/codex -run 'TestLoadRoleProfiles|TestAgentSetRole' -v`

Expected: FAIL because `loadRoleProfiles`, `core.AgentRole`, and `SetRole` do not exist.

- [ ] **Step 3: Implement the minimal role adapter**

```go
type AgentRole struct {
    Name, Description, Model, ReasoningEffort, DeveloperInstructions string
}

type RoleSwitcher interface {
    AvailableRoles() []AgentRole
    GetRole() string
    SetRole(name string) error
}
```

Read only direct `*.toml` children of `agent_profiles_dir`; decode the five supported fields; skip malformed or incomplete files with `slog.Warn`; sort valid roles by name. Preserve the Agent's base prompt and mode. `SetRole` replaces only model and normalized effort, and appends the role's developer instructions to the base prompt used by `newCodexSession`.

- [ ] **Step 4: Run the focused adapter tests**

Run: `go test ./agent/codex -run 'TestLoadRoleProfiles|TestAgentSetRole' -v`

Expected: PASS.

- [ ] **Step 5: Commit the adapter change**

```bash
git add agent/codex/role.go agent/codex/role_test.go agent/codex/codex.go core/interfaces.go
git commit -m "feat: load Codex agent roles"
```

### Task 2: Add the `/role` command and card navigation

**Files:**
- Modify: `core/engine.go`
- Modify: `core/engine_test.go`
- Modify: `core/i18n.go`

- [ ] **Step 1: Write failing command tests**

```go
func TestCmdRole_ListAndSwitchStartsNewSession(t *testing.T) {
    agent := &stubRoleAgent{roles: []AgentRole{{Name: "reviewer", Description: "Independent review", Model: "gpt-5.6-sol"}}}
    e, p, msg := newRoleCommandTestEngine(t, agent)
    e.cmdRole(p, msg, nil)
    if !strings.Contains(p.lastText(), "reviewer") { t.Fatal("role list missing reviewer") }

    oldID := e.sessions.GetOrCreateActive(msg.SessionKey).ID
    e.cmdRole(p, msg, []string{"switch", "reviewer"})
    if agent.current != "reviewer" { t.Fatal("role not selected") }
    if e.sessions.GetOrCreateActive(msg.SessionKey).ID == oldID { t.Fatal("role switch resumed the old session") }
}

func TestCmdRole_UnknownRoleDoesNotResetSession(t *testing.T) {
    // Set a known active session, request a missing profile, and assert that
    // the active session ID and selected role remain unchanged.
}
```

- [ ] **Step 2: Run core tests and confirm failure**

Run: `go test ./core -run 'TestCmdRole_' -v`

Expected: FAIL because `cmdRole` is not registered.

- [ ] **Step 3: Implement role selection**

```go
func (e *Engine) cmdRole(p Platform, msg *Message, args []string) {
    // With no args: list roles with numbered commands and buttons.
    // With "switch <number|name>": persist first, call SetRole, then
    // sessions.NewSession(msg.SessionKey, "") so old role context is not resumed.
}
```

Register `/role` in the normal command dispatcher, help output, and card action/navigation paths. Reuse the model-list presentation pattern for plain text, buttons, and cards. Add translated title, current role, usage, unsupported, unknown, save failure, and success strings in all current language maps.

- [ ] **Step 4: Run focused core tests**

Run: `go test ./core -run 'TestCmdRole_' -v`

Expected: PASS.

- [ ] **Step 5: Commit command support**

```bash
git add core/engine.go core/engine_test.go core/i18n.go
git commit -m "feat: add role switching command"
```

### Task 3: Persist the selected role per project

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`
- Modify: `cmd/cc-connect/main.go`
- Modify: `agent/codex/codex.go`
- Modify: `core/engine.go`

- [ ] **Step 1: Write a failing config test**

```go
func TestSaveAgentRole_PatchesOnlyTargetProjectOption(t *testing.T) {
    withConfig(t, twoProjectConfig(`
[projects.agent.options]
agent_profiles_dir = "/tmp/agents"
`))
    if err := SaveAgentRole("Enterprise-grade_RAG", "reviewer"); err != nil { t.Fatal(err) }
    if !strings.Contains(readConfigText(t), `agent_role = "reviewer"`) { t.Fatal("selected role was not persisted") }
}
```

- [ ] **Step 2: Run it and confirm failure**

Run: `go test ./config -run TestSaveAgentRole -v`

Expected: FAIL because `SaveAgentRole` does not exist.

- [ ] **Step 3: Implement config wiring**

```go
func SaveAgentRole(projectName, role string) error {
    configMu.Lock()
    defer configMu.Unlock()
    return patchProjectAgentOption(projectName, "agent_role", role)
}
```

In `cmd/cc-connect/main.go`, inject `engine.SetRoleSaveFunc` for `config.SaveAgentRole`. In `agent/codex.New`, load `agent_profiles_dir` and apply optional `agent_role`; an unavailable configured role logs a warning and retains the existing model behavior.

- [ ] **Step 4: Run all focused tests**

Run: `go test ./agent/codex ./config ./core -run 'Test(LoadRoleProfiles|AgentSetRole|CmdRole_|SaveAgentRole)' -v`

Expected: PASS.

- [ ] **Step 5: Commit persistence support**

```bash
git add config/config.go config/config_test.go cmd/cc-connect/main.go core/engine.go agent/codex/codex.go agent/codex/role.go
git commit -m "feat: persist selected Codex role"
```

### Task 4: Deploy to Enterprise-grade_RAG

**Files:**
- Modify: `/home/reggie/.cc-connect/config.toml`
- Replace: `/home/reggie/.nvm/versions/node/v22.21.0/lib/node_modules/cc-connect/bin/cc-connect`

- [ ] **Step 1: Build and test the binary**

Run: `go test ./agent/codex ./config ./core && go build -o /tmp/cc-connect-role ./cmd/cc-connect`

Expected: tests PASS and `/tmp/cc-connect-role --version` exits 0.

- [ ] **Step 2: Add the project-local role directory**

```toml
[projects.agent.options]
agent_profiles_dir = "/home/reggie/.codex/agents"
```

Do not set `agent_role`; the existing default model remains active until a role is selected on the phone.

- [ ] **Step 3: Install and restart**

```bash
install -m 0755 /tmp/cc-connect-role /home/reggie/.nvm/versions/node/v22.21.0/lib/node_modules/cc-connect/bin/cc-connect
systemctl --user restart cc-connect.service
```

- [ ] **Step 4: Verify live behavior**

Run: `systemctl --user is-active cc-connect.service && cc-connect --version && tail -n 80 /home/reggie/.cc-connect/logs/cc-connect.log`

Expected: service is `active`, configured profiles load, and both messaging platforms become ready. From a phone, `/role` includes `reviewer` and `code_developer`; switching to `reviewer` confirms a new session.
