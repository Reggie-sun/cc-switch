# Model Reasoning Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让手机端 `/model` 完整展示并安全切换当前 Model 与 model-specific Reasoning effort，同时保留 `/reasoning` 兼容入口和 workspace 隔离。

**Architecture:** `core.ModelOption` 承载 effort metadata，`agent/codex.parseCodexModelsJSON` 是映射的唯一 owner。`core.Engine` 用一组 agent-agnostic helper 消费 metadata，统一双 selector、显式 effort 切换、model switch 后的 default effort 校正和 `sessionKey` workspace 路由。

**Tech Stack:** Go、Codex model catalog/cache JSON、cc-connect card API、Go unit/CUJ tests、systemd user service。

---

## Problem Boundary

- Single owner: Codex catalog/cache metadata。
- Old path to replace: `normalizeReasoningEffort` / `AvailableReasoningEfforts` 的四项硬限制；仅渲染 model selector 的 `renderModelCard`; 使用 global `e.agent/e.sessions` 的 `renderReasoningCard()` 与 `/reasoning` card action。
- Unchanged contract: model switch 保留 agent session/history；显式 effort switch 清理 resolved session ID/history；直接 model name 仍可切换；无 metadata 的其他 agent/provider 回退自身 `AvailableReasoningEfforts()`。
- Out of scope: provider、role 行为（除 `max/ultra` normalization 回归）、permission mode、subagent 策略。

## Effort Rules

1. 显式选择必须属于当前 model 的 `ReasoningEfforts`; metadata 缺失/为空时使用 agent fallback。
2. `max`、`ultra` 是独立 canonical runtime value；文本兼容 `xhgh -> xhigh`、`urtal -> ultra`，卡片只发送 canonical value。
3. model switch 后，当前 effort 仍受支持则保留；否则使用新 model 声明且受支持的 `DefaultReasoningEffort`，不清 session/history。
4. default 缺失或不属于 supported set 时设置 `""`，交给 Codex runtime 选择默认值；不猜第一项，不降级为 `xhigh`。
5. 显式 unsupported effort 返回现有 usage/error，agent/session/history 均不变。

## File Structure

- Modify: `core/interfaces.go` — `ModelOption` metadata contract。
- Modify: `agent/codex/codex.go` — catalog/cache parser 与 normalization。
- Modify: `agent/codex/codex_cache_test.go`, `agent/codex/session_test.go`, `agent/codex/role_test.go` — Codex regressions。
- Modify: `core/engine.go`, `core/engine_test.go` — capability resolution、双 selector、action、default reconciliation、workspace tests。
- Modify: `core/i18n.go`, `core/cuj_test.go` — usage 与 CUJ-F2。
- Verify: `/home/reggie/.config/systemd/user/cc-connect.service`; deploy binary remains `/home/reggie/.local/bin/cc-connect-role`。

### Task 1: Parse Model-Specific Effort Metadata

**Files:**
- Modify: `core/interfaces.go:507-512`
- Modify: `agent/codex/codex.go:359-402`
- Test: `agent/codex/codex_cache_test.go`

- [ ] **Step 1: Write the failing parser test**

在 `agent/codex/codex_cache_test.go` 加入 `reflect` import 和：

```go
func TestParseCodexModelsJSON_PreservesReasoningMetadata(t *testing.T) {
	data := []byte(`{"models":[
		{"slug":"gpt-5.6-sol","visibility":"list","supported_in_api":true,
		 "default_reasoning_level":"low","supported_reasoning_levels":[
		 {"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}]},
		{"slug":"gpt-5.6-luna","visibility":"list","supported_in_api":true,
		 "default_reasoning_level":"medium","supported_reasoning_levels":[
		 {"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"}]},
		{"slug":"legacy","visibility":"list","supported_in_api":true}]}`)
	models := parseCodexModelsJSON(data)
	if len(models) != 3 { t.Fatalf("models=%#v", models) }
	if models[0].DefaultReasoningEffort != "low" || !reflect.DeepEqual(models[0].ReasoningEfforts, []string{"low","medium","high","xhigh","max","ultra"}) { t.Fatalf("sol=%#v", models[0]) }
	if models[1].DefaultReasoningEffort != "medium" || !reflect.DeepEqual(models[1].ReasoningEfforts, []string{"low","medium","high","xhigh","max"}) { t.Fatalf("luna=%#v", models[1]) }
	if models[2].DefaultReasoningEffort != "" || len(models[2].ReasoningEfforts) != 0 { t.Fatalf("legacy=%#v", models[2]) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./agent/codex -run TestParseCodexModelsJSON_PreservesReasoningMetadata -count=1`

Expected: compile FAIL because `ModelOption` lacks the metadata fields。

- [ ] **Step 3: Add the contract and parser**

```go
type ModelOption struct {
	Name string
	Desc string
	Alias string
	DefaultReasoningEffort string
	ReasoningEfforts []string
}
```

Add JSON fields to the parser payload:

```go
DefaultReasoningLevel string `json:"default_reasoning_level"`
SupportedReasoningLevels []struct { Effort string `json:"effort"` } `json:"supported_reasoning_levels"`
```

Before appending each model, normalize/deduplicate while preserving catalog order, then populate:

```go
efforts := make([]string, 0, len(m.SupportedReasoningLevels))
seenEfforts := map[string]struct{}{}
for _, level := range m.SupportedReasoningLevels {
	effort := normalizeReasoningEffort(level.Effort)
	if effort == "" { continue }
	if _, exists := seenEfforts[effort]; exists { continue }
	seenEfforts[effort] = struct{}{}
	efforts = append(efforts, effort)
}
models = append(models, core.ModelOption{Name:name, Desc:strings.TrimSpace(m.Description), DefaultReasoningEffort:normalizeReasoningEffort(m.DefaultReasoningLevel), ReasoningEfforts:efforts})
```

- [ ] **Step 4: Verify GREEN and commit**

```bash
go test ./agent/codex -run 'Test(ParseCodexModelsJSON|AvailableModels)' -count=1
git add core/interfaces.go agent/codex/codex.go agent/codex/codex_cache_test.go
git commit -m "feat: expose model reasoning metadata"
```

Expected: tests PASS; visibility/API filtering and catalog/cache priority stay unchanged。

### Task 2: Preserve Extended Codex Efforts

**Files:**
- Modify: `agent/codex/codex.go:189-249`
- Modify: `agent/codex/session_test.go:17-72`
- Modify: `agent/codex/role_test.go:32-42`

- [ ] **Step 1: Write failing normalization, CLI, and role tests**

Replace the narrow normalization test with:

```go
func TestNormalizeReasoningEffort(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{"low","low"},{"medium","medium"},{"high","high"},{"xhigh","xhigh"},{"xhgh","xhigh"},{"max","max"},{"ultra","ultra"},{"urtal","ultra"},{"minimal",""}} {
		t.Run(tt.in, func(t *testing.T) { if got := normalizeReasoningEffort(tt.in); got != tt.want { t.Fatalf("got %q want %q", got, tt.want) } })
	}
}
```

Add `TestBuildExecArgs_PreservesExtendedReasoningEffort` with subtests for `max` and `ultra`, asserting `containsSequence(args, []string{"-c", fmt.Sprintf("model_reasoning_effort=%q", effort)})`. Add `fmt` import. Replace the role application test with subtests whose profiles use `max` and `ultra` and assert `GetReasoningEffort()` equals the exact value。

- [ ] **Step 2: Verify RED**

Run: `go test ./agent/codex -run 'Test(NormalizeReasoningEffort|BuildExecArgs_PreservesExtendedReasoningEffort|AgentSetRolePreservesExtendedReasoningEffort)' -count=1`

Expected: FAIL because extended values and aliases normalize to empty。

- [ ] **Step 3: Extend only the Codex normalizer**

```go
func normalizeReasoningEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "": return ""
	case "low": return "low"
	case "medium", "med": return "medium"
	case "high": return "high"
	case "xhigh", "x-high", "very-high", "xhgh": return "xhigh"
	case "max": return "max"
	case "ultra", "urtal": return "ultra"
	default: return ""
	}
}
```

Keep `AvailableReasoningEfforts()` as the intentional four-item fallback。

- [ ] **Step 4: Verify GREEN and commit**

```bash
go test ./agent/codex -count=1
git add agent/codex/codex.go agent/codex/session_test.go agent/codex/role_test.go
git commit -m "fix: preserve extended Codex reasoning efforts"
```

### Task 3: Add Capability Resolution And Dual Selectors

**Files:**
- Modify: `core/engine.go:9465-9829,11881-12114,12840-12945`
- Modify: `core/engine_test.go:441-547,9323-9473`

- [ ] **Step 1: Write failing card tests**

Add `cardSelects(*Card) []CardSelect`, then table-test `renderModelCard` with `stubStrictModelAgent`: Sol expects six effort options, Luna five without `ultra`, and a legacy model expects the four-item agent fallback. Assert exactly two `CardSelect` elements and every effort value equals `act:/model effort <canonical>`。Add `TestHandleCardNav_ReasoningCardUsesWorkspaceAgent` using the binding setup from `TestHandleCardNav_ModelCardUsesWorkspaceAgent`, with global effort `low` and workspace effort `high`。

- [ ] **Step 2: Verify RED**

Run: `go test ./core/ -run 'Test(RenderModelCard_UsesModelSpecificReasoningEfforts|HandleCardNav_ReasoningCardUsesWorkspaceAgent)' -count=1`

Expected: FAIL because `/model` has one selector and `/reasoning` renders global state。

- [ ] **Step 3: Add shared resolution helpers**

```go
func reasoningEffortTarget(input string, efforts []string) (string, bool) {
	target := strings.ToLower(strings.TrimSpace(input))
	if target == "xhgh" { target = "xhigh" }
	if target == "urtal" { target = "ultra" }
	if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(efforts) { target = efforts[idx-1] }
	for _, effort := range efforts { if effort == target { return target, true } }
	return "", false
}

func (e *Engine) reasoningEffortOptions(agent Agent, known []ModelOption) ([]string, string) {
	rs, ok := agent.(ReasoningEffortSwitcher); if !ok { return nil, "" }
	fallback := append([]string(nil), rs.AvailableReasoningEfforts()...)
	ms, ok := agent.(ModelSwitcher); if !ok { return fallback, "" }
	models := known
	if models == nil { ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second); models = ms.AvailableModels(ctx); cancel() }
	for _, model := range models {
		if strings.EqualFold(model.Name, ms.GetModel()) {
			if len(model.ReasoningEfforts) == 0 { return fallback, model.DefaultReasoningEffort }
			return append([]string(nil), model.ReasoningEfforts...), model.DefaultReasoningEffort
		}
	}
	return fallback, ""
}
```

- [ ] **Step 4: Render two selectors and make reasoning navigation sessionKey-aware**

In `renderModelCard(sessionKey)`, reuse the already-fetched `models`, build `effortOpts` from `reasoningEffortOptions(agent, models)`, set canonical values `act:/model effort <effort>`, and chain:

```go
Select(e.i18n.T(MsgModelSelectPlaceholder), modelOpts, modelInit).
Select(e.i18n.T(MsgReasoningSelectPlaceholder), effortOpts, effortInit)
```

Change `renderReasoningCard()` to `renderReasoningCard(sessionKey string)`, resolve via `sessionContextForKey`, and use `reasoningEffortOptions`. Update `cmdReasoning` and `handleCardNav` call sites to pass `msg.SessionKey` / `sessionKey`。

- [ ] **Step 5: Verify GREEN and commit**

```bash
go test ./core/ -run 'Test(RenderModelCard_UsesModelSpecificReasoningEfforts|HandleCardNav_.*CardUsesWorkspaceAgent)' -count=1
git add core/engine.go core/engine_test.go
git commit -m "feat: render model-specific reasoning selector"
```

### Task 4: Unify Actions And Default Reconciliation

**Files:**
- Modify: `core/engine.go:9465-9829,9693-9741,11881-12114`
- Modify: `core/engine_test.go:4439-4520,5397-5480,9323-9473`

- [ ] **Step 1: Write failing action tests**

Add these exact behaviors:

- `TestCmdModel_EffortUsesModelMetadataAndResetsSession`: Sol + `/model effort urtal` becomes canonical `ultra`, clears resolved session ID/history。
- `TestCmdReasoning_RejectsEffortUnsupportedByCurrentModel`: Luna rejects `ultra`, preserving effort/session/history。
- `TestSwitchModelOnAgent_ReconcilesReasoningWithoutResettingSession`: current `max` stays on Luna; current `ultra` becomes Luna default `medium`; invalid/missing default becomes `""`; all preserve session ID/history。
- `TestHandleCardNav_ReasoningActionsUseWorkspaceContext`: both `act:/model effort ultra` and `act:/reasoning 2` mutate only workspace agent/sessions, never global state。

Update `TestCmdModel_DirectNameDoesNotNeedModelListMatch`: preserve direct-name acceptance but remove the zero `AvailableModels` call assertion because reconciliation needs a non-gating metadata lookup。

- [ ] **Step 2: Verify RED**

Run: `go test ./core/ -run 'Test.*(Model.*Effort|Reasoning.*Unsupported|ReconcilesReasoning|ReasoningActionsUseWorkspace)' -count=1`

Expected: FAIL on missing `/model effort`, missing reconciliation, and global card action routing。

- [ ] **Step 3: Add one explicit mutation helper**

```go
func (e *Engine) applyReasoningEffort(agent Agent, sessions *SessionManager, sessionKey, input string) (string, bool) {
	rs, ok := agent.(ReasoningEffortSwitcher); if !ok { return "", false }
	efforts, _ := e.reasoningEffortOptions(agent, nil)
	target, ok := reasoningEffortTarget(input, efforts); if !ok { return "", false }
	rs.SetReasoningEffort(target)
	e.cleanupInteractiveState(e.interactiveKeyForSessionKey(sessionKey))
	s := sessions.GetOrCreateActive(sessionKey)
	s.SetAgentSessionID("", "")
	s.ClearHistory()
	sessions.Save()
	return target, true
}
```

Use it from `cmdReasoning`, the early `cmdModel` `effort` branch, `handleModelCardAction` when fields are `effort <value>`, and `/reasoning` in `executeCardAction` after resolving `agent, sessions := e.sessionContextForKey(sessionKey)`。Text failures reply with `MsgReasoningUsage`; success replies with `MsgReasoningChanged`。No action path may use global `e.agent/e.sessions` directly。

- [ ] **Step 4: Reconcile once inside the common model switch owner**

```go
func (e *Engine) reconcileReasoningEffortForModel(agent Agent) {
	rs, ok := agent.(ReasoningEffortSwitcher); if !ok { return }
	efforts, defaultEffort := e.reasoningEffortOptions(agent, nil)
	current := strings.ToLower(strings.TrimSpace(rs.GetReasoningEffort()))
	if current == "" { return }
	if _, ok := reasoningEffortTarget(current, efforts); ok { return }
	if target, ok := reasoningEffortTarget(defaultEffort, efforts); ok { rs.SetReasoningEffort(target); return }
	rs.SetReasoningEffort("")
}
```

Call it after every successful `SetModel`/provider update in `switchModelOnAgent`, before returning success。Do not call `applyReasoningEffort`; automatic correction must preserve session continuity。This common owner covers text, synchronous card, and async card callers。

- [ ] **Step 5: Verify GREEN and commit**

```bash
go test ./core/ -run 'Test.*(Model|Reasoning)' -count=1
git add core/engine.go core/engine_test.go
git commit -m "fix: map reasoning actions to the active model"
```

### Task 5: Update Usage And CUJ-F2

**Files:**
- Modify: `core/i18n.go:2474-2520`
- Modify: `core/cuj_test.go:47-97,1732-1745`

- [ ] **Step 1: Write a real RED CUJ-F2**

Add these fields and methods to `cujAgent`:

```go
model           string
reasoningEffort string
models          []ModelOption

func (a *cujAgent) SetModel(model string) { a.model = model }
func (a *cujAgent) GetModel() string { return a.model }
func (a *cujAgent) AvailableModels(context.Context) []ModelOption {
	return append([]ModelOption(nil), a.models...)
}
func (a *cujAgent) SetReasoningEffort(effort string) { a.reasoningEffort = effort }
func (a *cujAgent) GetReasoningEffort() string { return a.reasoningEffort }
func (a *cujAgent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh"}
}
```

Replace the linked marker with this real `ReceiveMessage` journey:

```go
func TestCUJ_F2_ModelAndReasoningMapping(t *testing.T) {
	env := newCUJEnv(t)
	env.agent.model = "gpt-5.6-sol"
	env.agent.reasoningEffort = "high"
	env.agent.models = []ModelOption{
		{Name: "gpt-5.6-sol", DefaultReasoningEffort: "low", ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{Name: "gpt-5.6-luna", DefaultReasoningEffort: "medium", ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}},
	}

	env.userSends("f2", "/model")
	env.waitFor("model list", 2*time.Second, func() bool { return len(env.plat.getSent()) >= 1 })
	if got := env.plat.getSent()[0]; !strings.Contains(got, "gpt-5.6-sol") || !strings.Contains(got, "gpt-5.6-luna") {
		t.Fatalf("model list = %q", got)
	}

	env.plat.clearSent()
	env.userSends("f2", "/model effort ultra")
	env.waitFor("effort switch", 2*time.Second, func() bool { return len(env.plat.getSent()) >= 1 })
	if got := env.plat.getSent()[0]; !strings.Contains(got, "ultra") { t.Fatalf("effort reply = %q", got) }

	env.plat.clearSent()
	env.userSends("f2", "/model switch 2")
	env.waitFor("model switch", 2*time.Second, func() bool { return len(env.plat.getSent()) >= 1 })
	if env.agent.model != "gpt-5.6-luna" || env.agent.reasoningEffort != "medium" {
		t.Fatalf("model=%q effort=%q", env.agent.model, env.agent.reasoningEffort)
	}

	env.plat.clearSent()
	env.userSends("f2", "/reasoning")
	env.waitFor("reasoning list", 2*time.Second, func() bool { return len(env.plat.getSent()) >= 1 })
	if got := env.plat.getSent()[0]; strings.Contains(got, "ultra") || !strings.Contains(got, "max") {
		t.Fatalf("luna reasoning list = %q", got)
	}
}
```

- [ ] **Step 2: Verify CUJ RED**

Run: `go test ./core/ -run TestCUJ_F2_ModelAndReasoningMapping -count=1`

Expected: FAIL until the CUJ agent capabilities and command flow exist。

- [ ] **Step 3: Update five-language usage strings**

`MsgModelUsage` must mention `/model switch <...>` and `/model effort <effort>`。`MsgReasoningUsage` must list `low|medium|high|xhigh|max|ultra` without advertising misspellings。Reuse existing model/reasoning select placeholder keys; add no new user-facing key。

- [ ] **Step 4: Verify GREEN and commit**

```bash
go test ./core/ -run 'TestCUJ_F2_ModelAndReasoningMapping|TestI18n' -count=1
go test ./core/ -run TestCUJ -count=1
git add core/i18n.go core/cuj_test.go
git commit -m "test: cover model reasoning user journey"
```

### Task 6: Independent Review, Full Gates, Deploy, And Phone Smoke

**Files:**
- Review: all files changed in Tasks 1-5。
- Verify: `/home/reggie/.config/systemd/user/cc-connect.service`
- Deploy: `/home/reggie/.local/bin/cc-connect-role`

- [ ] **Step 1: Run focused gates**

```bash
go test ./agent/codex -count=1
go test ./core/ -run 'Test.*(Model|Reasoning)' -count=1
go test ./core/ -run TestCUJ -count=1
```

Expected: all PASS without cached masking。

- [ ] **Step 2: Dispatch an independent read-only reviewer**

Use this exact contract:

```text
Role: reviewer. Authority: read-only. Review the diff against docs/superpowers/specs/2026-07-16-model-reasoning-mapping-design.md. Verify catalog single ownership; Sol/Terra six efforts; Luna no ultra; max/ultra and aliases; explicit reset versus automatic continuity; missing metadata fallback; unsupported/default rules; sessionKey-aware /model and /reasoning; direct model-name compatibility; and no provider/role/permission/subagent expansion. Do not edit files or spawn subagents. Return verdict, blocking issues, non-blocking concerns, exact file/symbol evidence, tests inspected, and minimal follow-up.
```

Expected: `accept` or `accept with concerns` and no blocking issue。Parent directly verifies every important claim; a real blocker gets a failing regression, minimal fix, focused rerun, and scoped commit。

- [ ] **Step 3: Run full tests, build, and final diff checks**

```bash
go test -p 1 ./...
go build -buildvcs=false ./...
git diff --check
git status --short --branch
git log --oneline --decorate -8
```

Expected: tests/build PASS, `git diff --check` silent, no unrelated staged or modified files。

- [ ] **Step 4: Build and deploy the exact service binary**

```bash
go build -buildvcs=false -o /tmp/cc-connect-role ./cmd/cc-connect
systemctl --user show cc-connect.service --property=ExecStart --value
install -m 0755 /tmp/cc-connect-role /home/reggie/.local/bin/cc-connect-role
systemctl --user restart cc-connect.service
systemctl --user is-active cc-connect.service
systemctl --user show cc-connect.service --property=ExecStart,MainPID --no-pager
journalctl --user -u cc-connect.service -n 30 --no-pager
```

Expected: `ExecStart=/home/reggie/.local/bin/cc-connect-role`, service `active`, non-zero `MainPID`, fresh Feishu websocket connection log。

- [ ] **Step 5: Perform phone smoke in a workspace-bound chat**

Open `/model` and confirm two selectors；Sol/Terra each show `low/medium/high/xhigh/max/ultra`; Luna shows five without `ultra`。Choose Sol `ultra`, switch to Luna, reopen `/model`, and confirm `medium` while the conversation remains resumable。Use `/reasoning max` and confirm the same effort state。Repeat an effort action in a workspace-bound chat and verify global/unbound model, effort, session ID, and history remain untouched。

- [ ] **Step 6: Record verified evidence**

Final handoff records reviewer verdict, exact commands, model/effort counts, service identity, websocket evidence, and phone observations。If phone access is unavailable, report it as the remaining runtime blocker and do not claim mobile smoke verified。
