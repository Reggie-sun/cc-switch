# Codex Role Switching Design

## Goal

让 `Enterprise-grade_RAG` 的 cc-connect 手机入口可以选择本机现有的 Codex agent profile，并尽量复用其既有工作模式，而不是只暴露原始模型名称。

## Scope

- 新增 Codex 专用的 `/role` 命令和交互菜单。
- 仅当项目配置 `agent_profiles_dir` 时启用；当前部署只为 `Enterprise-grade_RAG` 配置 `/home/reggie/.codex/agents`。
- 运行时读取 profile TOML 的 `name`、`description`、`model`、`model_reasoning_effort`、`developer_instructions`。
- 选择角色后创建新会话，并将 profile 的模型、推理强度和开发指令应用于该会话。
- `/model` 继续是原始模型切换入口，不改变其当前语义。
- 当前项目的 `mode = "yolo"` 保持不变；profile 的 `sandbox_mode` 只作为 profile 元数据读取，不改变 IM 工作流的执行权限。

## Data Flow

1. cc-connect 启动时，Codex agent 从 `agent_profiles_dir` 加载有效 profile。
2. 用户发送 `/role`，桥接展示 profile 名称与描述，并提供可选择的编号或按钮。
3. 用户选择某个 profile，桥接保存选中的 profile 名称到项目配置。
4. 当前会话保留在历史列表中；桥接为该用户创建新会话。
5. 新会话使用 profile 的 `model` 与 `model_reasoning_effort`，并在首条用户消息前注入 `developer_instructions`。

## Boundaries

- 不复制、迁移或修改 `/home/reggie/.codex/agents/*.toml`。
- 不修改全局 `~/.codex/config.toml`、模型缓存或登录态。
- 不把 profile 选择伪装成 `/model`，避免破坏现有的直接模型切换。
- 不自动根据当前模型推断默认 profile；未选择角色时维持现有项目配置行为。
- 不在已有会话中热切换角色指令，避免新旧角色指令混入同一上下文。

## Failure Handling

- profile 目录不存在、文件不可读或 TOML 无效时，跳过该 profile 并记录结构化警告。
- 缺少 `name`、`model` 或 `developer_instructions` 的 profile 不显示在角色菜单中。
- `/role switch <name>` 找不到 profile 时返回可见错误，不变更当前会话或项目配置。
- 持久化失败时不启动新会话，防止运行时选择与重启后的配置漂移。

## Verification

- 为 profile 解析、排序、无效条目过滤和角色切换添加单元测试。
- 为 `/role` 菜单、未知角色、切换后新会话与配置持久化添加 core 命令测试。
- 运行 `go test ./agent/codex ./core/`，然后构建二进制并以现有 user service 做一次启动与角色菜单 smoke。
