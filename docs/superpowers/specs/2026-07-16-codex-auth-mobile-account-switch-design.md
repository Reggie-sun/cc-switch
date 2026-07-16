# Codex Auth Mobile Account Switching Design

**Date:** 2026-07-16
**Status:** Approved in conversation; awaiting written-spec review
**Scope:** 手机端 Codex 账号额度查看、手动刷新与全局账号切换

## Goal

在 cc-connect 的手机聊天入口中增加 admin-only `/account` 管理卡，使管理员可以：

1. 查看本机 `codex-auth` 已登记账号的脱敏身份、Plan、5 小时与 weekly 剩余额度；
2. 在明确触发时刷新全部账号额度；
3. 经过二次确认后全局切换 active Codex 账号；
4. 切换后安全退役旧账号的所有 live Codex sessions，并在新账号下创建新 thread；
5. 保持 cc-connect 本地 session history，不把旧账号的 agent thread ID 带入新账号。

## Problem Boundary

- Single owner：`agent/codex` 拥有 `codex-auth`、registry、effective `CODEX_HOME` 与账号切换实现。
- Engine owner：`core.Engine` 拥有 command/card ACL、确认态、切换 barrier、active-turn gate 与同 auth scope session retirement。
- Existing read path：`/usage` 只报告当前 `auth.json` 对应账号，不枚举或切换账号。
- Old manual path：用户在主机执行 `codex-auth switch <query>`，随后自行重启 Codex client。
- New path：手机 `/account` 通过 typed capability 调用相同行为，并补齐 daemon 内的 session retirement。
- Unchanged contracts：`/usage` 的 provider-neutral quota contract、workspace model/reasoning、provider、role、permission、subagent 策略均不改变。
- Out of scope：账号 login/import/remove、auto-switch 配置、per-workspace `CODEX_HOME` 隔离、自动选择“额度最多”账号、provider 账号切换、在非 Codex agent 上模拟账号能力。

## Confirmed Product Decisions

- Account scope：全局 effective `CODEX_HOME`，不是 chat-local 或 workspace-local。
- Thread policy：切换后强制新 agent thread；清空相关 resume IDs，但保留本地 history。
- Active-turn policy：存在同 auth scope 的 active Codex turn 时阻止切换，不自动取消、不排队。
- Quota refresh：打开卡片只读缓存；管理员显式点击 Refresh 才刷新全部账号 API。
- Account label：alias 优先，脱敏邮箱兜底；卡片与 action 不包含完整邮箱。
- UI：独立 admin-only `/account` 卡片；`/usage` 只增加 Accounts 入口。
- Confirmation：选择账号不产生 mutation；必须经过独立确认卡。

## Existing Evidence

本机 `codex-auth 0.2.10` 提供：

- `codex-auth list [--api|--skip-api]`
- `codex-auth switch <query>`
- `codex-auth status`

`codex-auth list --skip-api` 当前可列出 active marker、Plan、5h/weekly usage 与 last activity。已安装包文档明确说明普通 Codex CLI/App 切换账号后需要 restart client。cc-connect 的 Codex CLI/app-server sessions 是长生命周期进程，因此只改 `auth.json` 会形成 `/usage` 已显示新账号、live turn 仍使用旧账号的错配。

当前 `agent/codex/usage.go` 每次从 `auth.json` 读取 OAuth token，再请求 active account usage。该路径只使用进程 `CODEX_HOME`/默认 `~/.codex`；新能力必须统一 effective auth home，避免 configured `codex_home` 与 `/usage`、`codex-auth` 指向不同目录。

## Architecture

### Provider-Neutral Core Contract

`core` 新增最小可选 capability；字段只承载脱敏、可展示信息：

```go
type AccountOption struct {
	ID                     string
	Label                  string
	Plan                   string
	Active                 bool
	FiveHourRemaining      *int
	WeeklyRemaining        *int
	UsageUpdatedAt         time.Time
}

type AccountSwitchResult struct {
	Account            AccountOption
	AuthMayHaveChanged bool
	Verified           bool
}

type AccountSwitcher interface {
	AccountAuthScope() string
	ListAccounts(ctx context.Context, refresh bool) ([]AccountOption, error)
	SwitchAccount(ctx context.Context, accountID string) (AccountSwitchResult, error)
}
```

Rules：

- `ID` 是 opaque value，仅供同一 process 内回传；不得是 email、token 或可执行 query。它在一次 process 生命周期内对同一 registry record 稳定，service restart 后旧 card ID 必须失效。
- `Label` 已在 agent boundary 完成 alias/脱敏处理；`core` 不再次解析账号身份。
- percentage 字段表达 remaining，不沿用 `codex-auth` 的 used-percent 命名。
- `AuthMayHaveChanged` 允许 Engine 在 post-verify 失败时采取 fail-safe retirement。
- `AccountAuthScope` 返回稳定但不展示的 scope key；共享同一 effective `CODEX_HOME` 的 workspace clones 必须返回同一值。

### Codex Account Adapter

`agent/codex` 实现 `AccountSwitcher`：

1. 通过 Agent 的 configured `codexHome`、进程 `CODEX_HOME` 与 home default 计算单一 effective auth home。
2. cached list 直接读取 `<effective-home>/accounts/registry.json`，不进行 API call。
3. refresh list 使用 `exec.CommandContext` 固定 argv 调用 `codex-auth list --api`，关闭 stdin、使用 20 秒 timeout，并把 stdout/stderr 各限制为 8 KiB，然后重新读取 registry。
4. registry parser 只提取 active key、alias、email、Plan、last usage、updated time；不读取、返回或记录 token。
5. 对 email 做 masking；有 alias 时使用 `alias (m***@domain)`，无 alias 时只显示 masked email。
6. `SwitchAccount` 重新读取 registry，以 opaque ID 唯一解析记录；用户输入永不直接成为 CLI query。
7. adapter 选择该记录的唯一完整标识调用 `codex-auth switch <identifier>`；禁止 shell，stdin 关闭，使用 10 秒 timeout，并把 stdout/stderr 各限制为 8 KiB。
8. CLI 返回后重新读取 registry，确认 `active_account_key` 与目标一致。
9. CLI 非零退出且无成功证据时返回 `AuthMayHaveChanged=false`；CLI 成功但复核失败或不一致时返回 `AuthMayHaveChanged=true, Verified=false`。

`agent/codex/usage.go` 同时复用 effective auth home helper，使 `/usage` 与 `/account` 始终指向同一 auth scope。

### Engine Account Coordinator

`core.Engine` 新增 account-switch coordinator：

- mutex：串行化 refresh 与 switch execution；
- per-scope switching flag：在确认开始后阻止同 scope 新 Codex turn 启动；
- confirmation state：按发起 session 保存 target opaque ID、脱敏 label、创建时间；5 分钟 TTL 后失效，service restart 后不恢复；
- retirement owner：snapshot matching live states，在 Engine lock 外调用现有 `cleanupInteractiveState(key, expectedState)`；
- session reset owner：遍历 global 与 workspace session managers，只处理 agent 实现 `AccountSwitcher` 且 `AccountAuthScope` 匹配的 manager，清空 `AgentSessionID` 并保存；history、session name 与 past visibility 保留。

切换步骤：

1. callback 再次验证 admin；
2. 获取 switch mutex 并设置 scope switching flag；
3. snapshot 同 scope active turns；`Session.Busy()`、未解决 permission 或非空 queued messages 任一成立都计为 active。若数量大于零，清 flag 并返回 blocked result；
4. 调用 `SwitchAccount`；
5. 若 `AuthMayHaveChanged=false`，不 retire；
6. 若 `AuthMayHaveChanged=true`，无论 post-verify 是否成功都 retire live sessions、清 resume IDs；
7. 保存受影响 session managers；
8. 清 switching flag；
9. Verified 时显示 success；未 Verified 时显示 auth-state-uncertain，不宣称成功。

消息进入 StartSession 前必须检查同 scope switching flag。该 barrier 先于 active-turn snapshot 建立，防止“检查为空后又启动新 turn”的竞态。

## Authorization And Secret Safety

`/account` 是 privileged command：

- 加入 `privilegedCommands`，复用 `admin_from`；empty `admin_from` 保持 deny-all。
- 文本 command 通过既有 `handleCommand` ACL。
- `handleCardNav`/account action handler 必须独立调用 `isAdmin(extractUserID(sessionKey))`，因为 card callback 不经过文本 command dispatch。
- `/usage` 只对 admin 展示 Accounts 入口；非 admin 直接构造 account callback 仍被拒绝。
- catalog、Refresh、select、confirm 全部 admin-only，不仅保护最终 switch。
- 不输出 full email、registry key、auth path、token、raw CLI stdout/stderr。
- 日志只记录 project、admin user ID、action outcome、retired count 与脱敏 label。

## Mobile Interaction

### Account List Card

`/account`：

- title：Codex Accounts；
- active account summary；
- account selector，每项显示脱敏 label、Plan、cached 5h/weekly remaining 与更新时间；
- cached/stale 提示；
- Refresh quotas、Back 按钮。

选择 selector 后只写 confirmation state，并返回确认卡；不调用 CLI，不退役 session。

### Confirmation Card

确认卡显示：

- target 脱敏 label；
- “global auth switch” 明示；
- 将关闭所有同 scope live Codex sessions；
- 将清空 resume IDs 并在新账号创建新 thread；
- 本地 history 保留；
- Confirm、Cancel。

### Result Cards

- Blocked：显示 active turn 数量，提示全部结束后重试。
- Success：显示新 active label、retired live session 数、cleared resume 数；说明下一条消息创建新 thread；提供 Usage 与 Back。
- Failed unchanged：显示脱敏原因，说明 auth/session 未改变。
- Auth uncertain：说明 CLI 可能已改变 auth、sessions 已安全退役，要求重新打开 `/account` 核实。

### Usage Integration

`/usage` 保持 provider-neutral report。仅当 resolved agent 实现 `AccountSwitcher` 且用户为 admin 时，usage card 增加 Accounts button。该入口只导航到 `/account`，不把账号 mutation 放入 usage renderer。

### Text Fallback

无卡平台支持：

- `/account`：编号列表与 cached 标记；
- `/account refresh`：显式刷新；
- `/account switch <number>`：建立短期确认态并返回警告；
- `/account confirm`：执行当前有效确认态；
- `/account cancel`：清确认态。

文本 fallback 不接受 email/alias 作为直接 switch query，避免 fuzzy ambiguity 与敏感信息进入聊天记录。

## Error Handling

- Unsupported agent：返回 account switching not supported。
- Missing binary：明确提示主机未安装 `codex-auth`，不回显 PATH。
- Missing/malformed registry：提示运行主机侧 `codex-auth import --purge`，不显示 registry 内容。
- Refresh timeout/API failure：保留旧 cached list，卡片标注 refresh failed；不清缓存。
- Ambiguous/missing opaque ID：确认态失效，要求重开 `/account`。
- Active turns：阻止切换，不 mutation。
- CLI failure before possible mutation：不 retire。
- Post-switch verification failure：标记 uncertain 并 fail-safe retire。
- Session close timeout：继续处理其他 sessions，在结果中记录失败数量；不恢复旧 auth。
- Engine shutdown：取消 refresh/switch context，清 confirmation state。

## Concurrency And Consistency

- switch mutex 防两个管理员同时确认不同账号。
- per-scope barrier 防 switch window 内启动新 Codex process。
- active-turn check 在 barrier 建立后执行。
- interactive state snapshot 在 `interactiveMu` 下完成，close 在锁外完成；使用 expected-state identity 防止 stale cleanup 删除 replacement state。
- workspace pool 与 session managers 使用各自 snapshot API；不在 workspace pool lock 下保存或 close。
- registry list 与 switch verification 都在 agent boundary 完成；Engine 不缓存 raw registry records。

## TDD Verification

### Agent Tests

- effective auth home precedence 与 `/usage` 共用 helper；
- registry parsing、used-to-remaining conversion、active marker、timestamp；
- alias + masked email，无 full email/token 泄露；
- cached list 不执行 CLI；refresh 使用 exact argv/environment/timeout/output cap；
- opaque ID 唯一解析，unknown/ambiguous input 拒绝；
- switch exact argv、stdin closed、CLI failure、timeout；
- verified success 与 auth-state-uncertain result。

### Core Tests

- `/account` 注册、help/i18n、disabled command；
- text ACL 与 card callback ACL 均 fail closed；
- non-admin 看不到 Usage Accounts entry；
- list/refresh 使用 sessionKey-resolved workspace agent；
- select 不 mutation，confirmation TTL/cancel；
- active-turn gate 阻止且不调用 switch；
- switch barrier 阻止新 turn；
- success retire 全部 matching-scope live states；
- matching global/workspace resume IDs 清空、history/name 保留并保存；
- different auth scope 与 non-Codex agents 不受影响；
- uncertain result 仍 retire；unchanged failure 不 retire；
- concurrent confirms 串行且 stale confirm 不执行。

### CUJ And Runtime

- 真实 `ReceiveMessage` + card actions：list → refresh → select → confirm → next message new thread；
- 用户可见 card 内容不包含 full email、registry key 或 raw CLI error；
- focused agent/core tests、race tests、full suite、build；
- independent reviewer；
- build exact service binary、systemd restart、fresh Feishu websocket；
- 手机 smoke：cached quota、manual Refresh、non-admin rejection、active-turn block、confirmed switch、new thread、`/usage` active account consistency。

## Deployment And Rollback

部署沿用 `cc-connect.service` 与 `/home/reggie/.local/bin/cc-connect-role`。部署前记录旧 binary SHA；失败时恢复旧 binary 并 restart service。

账号切换本身不提供自动 rollback：一旦 `codex-auth` 可能已修改 global auth，系统只做 fail-safe session retirement，并要求管理员重新进入 `/account` 明确选择目标账号。

## Success Criteria

- 管理员可在手机上看见脱敏多账号额度并手动刷新；
- 未确认选择绝不修改 auth；
- 非管理员无法列举或切换账号，包括伪造 card callback；
- active turn 存在时绝不切换；
- verified switch 后所有同 auth scope live sessions 被退役、resume IDs 清空、本地 history 保留；
- 下一条消息在新账号创建新 thread；
- `/usage` 与 `/account` 显示同一 active account；
- 不泄露 token、full email、registry key、auth path 或 raw CLI output；
- 不改变 provider、role、permission mode 或 subagent 行为。
