# CC-Connect Native Role Delegation Design

## Decision

手机 `cc-connect` 会话保持根角色，不让用户通过 `/role switch` 选择单一角色。根会话根据任务和现有 `AGENTS.md` 规则，按需调用 Codex 原生 custom agents，并自行汇总结果。

## Scope

- 仅影响 `Enterprise-grade_RAG` 的 `cc-connect` 项目。
- 使用现有 `~/.codex/agents/*.toml` 与项目 `.codex/agents/*.toml` 的原生加载机制。
- 不修改全局 `~/.codex/config.toml`，不启用全局 proactive multi-agent 模式，不启动 cc-connect 自行编排的独立子进程。
- `/role` 保留为诊断和手动兜底，不作为正常工作流。

## Configuration

在 `~/.cc-connect/config.toml` 的项目 Agent options 增加 `append_system_prompt`。它将明确要求手机根会话：

1. 把本机和项目 custom agents 视为原生可调用子代理。
2. 依据现有 `AGENTS.md` 的触发条件选择最小、匹配的角色。
3. 简单或强上下文耦合任务留在主线程；不为展示角色而 fan-out。
4. 子代理不得递归派发，父会话负责最终整合，并在最终答复说明必要的委派。

cc-connect 已通过 `codex exec` 且不使用 `--ignore-user-config` 启动，因此 Codex 自行加载 custom agent TOML，并在派生 session 时应用其模型、推理强度和 developer instructions。

## Runtime Behavior

`append_system_prompt` 只会在新的 Codex thread 建立时作为 preamble 注入。部署并重启服务后，用户发送一次 `/new`；该新会话之后的普通任务即可按需调用 profile。

当前线程和桌面 Codex 配置保持不变。默认 `agents.max_depth = 1` 的直接子代理边界不变。

## Validation

- 使用 TOML 配置加载和服务启动验证，确认服务仍连接平台。
- 新会话发送一个涉及多个独立阅读面或高风险复核的真实任务，确认根会话显示原生子代理活动并完成汇总。
- 不把单纯的 `/role` 列表当作子代理调用证明。

## Risks

子代理会增加 token、时延和本机资源消耗。提示词要求按现有规则做条件委派，避免无差别并发；是否派发仍由 Codex 根据任务边界判断，不承诺每条消息都有子代理。
