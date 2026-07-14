# CC-Connect Native Role Delegation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让手机 cc-connect 根会话按现有规则自主调用 Codex 原生 custom agents，而不要求用户手动切换角色。

**Architecture:** 仅在 `~/.cc-connect/config.toml` 的 `Enterprise-grade_RAG` Agent options 追加一个 scoped `append_system_prompt`。cc-connect 继续通过 `codex exec` 使用默认 `CODEX_HOME`，由 Codex 原生加载项目和用户的 custom agent TOML；协调指令只规定何时按需委派，不创建新的多代理控制面。

**Tech Stack:** TOML、systemd user service、Codex CLI custom agents。

---

## File Structure

- Modify: `~/.cc-connect/config.toml` — 仅为 `Enterprise-grade_RAG` 添加手机会话协调指令。
- Verify: `~/.config/systemd/user/cc-connect.service` — 保持现有自定义二进制和服务范围不变。
- No source code changes — cc-connect 已使用 `codex exec`，Codex 已原生加载 `~/.codex/agents/*.toml`。

### Task 1: Add the scoped coordination instruction

**Files:**
- Modify: `~/.cc-connect/config.toml:13-21`
- Test: `systemctl --user status cc-connect.service`

- [ ] **Step 1: Confirm the target project has no existing coordinator prompt**

Run:

```bash
sed -n '9,24p' /home/reggie/.cc-connect/config.toml
```

Expected: the `Enterprise-grade_RAG` `projects.agent.options` table contains `work_dir`, model/mode settings, and `agent_profiles_dir`, but no `append_system_prompt`.

- [ ] **Step 2: Add `append_system_prompt` under the target Agent options**

Add exactly this TOML value after `agent_profiles_dir`:

```toml
append_system_prompt = """
You are the coordinator for this cc-connect session. Treat the native custom agents available from the project and /home/reggie/.codex/agents as callable subagents, not as roles the user must switch manually. Apply the active AGENTS.md delegation policy: choose the narrowest matching profile only when independent exploration, verification, or bounded implementation materially improves the result; keep simple or tightly coupled work in the parent thread. Do not fan out merely to display roles. Subagents must not recursively delegate, and you remain responsible for integrating their results and reporting material delegation in the final answer. Do not ask the user to run /role or choose a role for normal work.
"""
```

- [ ] **Step 3: Inspect the edited configuration**

Run:

```bash
sed -n '13,36p' /home/reggie/.cc-connect/config.toml
```

Expected: only the target project contains the new prompt; global Codex config and other projects are unchanged.

### Task 2: Restart and validate the scoped runtime

**Files:**
- Verify: `~/.config/systemd/user/cc-connect.service`
- Verify: `~/.cc-connect/logs/cc-connect.log`

- [ ] **Step 1: Restart the user service**

Run:

```bash
systemctl --user restart cc-connect.service
systemctl --user is-active cc-connect.service
```

Expected: `active`.

- [ ] **Step 2: Confirm the service still launches the scoped custom binary and reconnects**

Run:

```bash
systemctl --user show cc-connect.service --property=ExecStart --value
journalctl --user -u cc-connect.service -n 20 --no-pager
```

Expected: `ExecStart` remains `/home/reggie/.local/bin/cc-connect-role`; logs show the fresh process and its platform connection.

- [ ] **Step 3: Start a new phone thread and perform the live confirmation**

From the phone, send:

```text
/new
```

Then send one task that has at least two independent investigation or verification dimensions. Confirm the root session performs the task through native subagents when the active `AGENTS.md` trigger applies, then returns one integrated response. A simple question may correctly stay single-threaded.

### Task 3: Record the configuration-only delivery

**Files:**
- Verify: `/tmp/cc-connect-src/docs/superpowers/specs/2026-07-14-cc-connect-native-role-delegation-design.md`
- Verify: `/tmp/cc-connect-src/docs/superpowers/plans/2026-07-14-cc-connect-native-role-delegation.md`

- [ ] **Step 1: Confirm source code is unchanged by this implementation**

Run:

```bash
git -C /tmp/cc-connect-src status --short
```

Expected: only the committed design and plan documents belong to this delivery; no new Go source change is required.

- [ ] **Step 2: Commit the plan document**

Run:

```bash
git -C /tmp/cc-connect-src add docs/superpowers/plans/2026-07-14-cc-connect-native-role-delegation.md
git -C /tmp/cc-connect-src commit -m "docs: plan native role delegation"
```

Expected: one documentation-only commit; runtime configuration remains outside the source repository.
