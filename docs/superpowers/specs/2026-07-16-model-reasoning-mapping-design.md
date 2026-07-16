# Model Reasoning Mapping Design

## Context

当前手机端 `/model` 只展示模型选择，推理强度被拆到 `/reasoning`。Codex adapter 还把可选强度硬编码为 `low / medium / high / xhigh`，因此本机模型目录中真实存在的 `max / ultra` 会被丢弃，role profile 中的这两个值也会被归一化为空。

本机 Codex model metadata 已提供完整映射：

- `gpt-5.6-sol`、`gpt-5.6-terra`：`low / medium / high / xhigh / max / ultra`
- `gpt-5.6-luna`：`low / medium / high / xhigh / max`
- `gpt-5.5`、`gpt-5.4`、`gpt-5.4-mini`、Spark、Auto Review：`low / medium / high / xhigh`

`max` 和 `ultra` 是独立 runtime 值，不映射为 `xhigh`。

## Decision

采用双选择器设计：手机端 `/model` 卡片同时展示 Model 和 Reasoning effort。Reasoning effort 根据当前模型的 metadata 动态生成；`/reasoning` 保留为兼容快捷入口，并复用同一套能力判断和切换逻辑。

不采用模型与强度的笛卡尔积选项，因为它会把少量模型扩展为几十个重复条目；也不只扩展 `/reasoning`，因为那仍无法在用户主要使用的 `/model` 页面表达完整映射。

## Problem Boundary

- Single owner：Codex model catalog metadata 是模型与 reasoning effort 映射的唯一事实来源。
- Old path to replace：`normalizeReasoningEffort` 和 `AvailableReasoningEfforts` 的四项硬编码，以及 `/model` 卡片只渲染 model selector 的路径。
- Unchanged contracts：模型切换继续沿用现有 persistence/session continuity；显式切换 effort 继续清理旧 agent session/history；`/reasoning` 文本指令保持兼容；其他 agent 在没有 model-specific metadata 时继续使用自身 `AvailableReasoningEfforts()`。
- Scope：只改 model metadata、Codex effort normalization、`/model`/`/reasoning` UI/action 和相关测试，不改变 provider、role、permission mode 或 subagent 策略。

## Data Model

扩展 `core.ModelOption`：

- `DefaultReasoningEffort string`
- `ReasoningEfforts []string`

`agent/codex.parseCodexModelsJSON` 从 `default_reasoning_level` 与 `supported_reasoning_levels[].effort` 填充这两个字段。字段缺失时保持空值，使其他 provider 和旧 catalog 自然回退。

Codex normalization 接受 canonical values：`low / medium / high / xhigh / max / ultra`。文本输入额外兼容用户常见拼写 `xhgh -> xhigh`、`urtal -> ultra`；卡片只显示 canonical values。

## UI And Action Flow

`renderModelCard(sessionKey)` 使用 `sessionContextForKey(sessionKey)` 获取当前 workspace agent，然后：

1. 加载 model options 并渲染现有 model selector。
2. 查找当前 model 对应的 `ReasoningEfforts`。
3. 如果 metadata 存在，渲染该模型的动态 effort selector；否则回退到 agent 的 `AvailableReasoningEfforts()`。
4. effort action 使用 `/model effort <value>`，在当前 `sessionKey` 对应的 agent/session 上执行，完成后返回更新后的 `/model` 卡片。

`/reasoning` 卡片和 card action 同样改为 sessionKey-aware，修复手机 workspace 操作错误落到 global agent/session 的现有问题。

## Compatibility Rules

- 选择 effort：必须属于当前模型声明的集合；若 model metadata 不可用，则使用 agent fallback 集合。
- 切换模型：如果当前 effort 仍被新模型支持则保留；如果不支持，则切到新模型的 `default_reasoning_level`。该自动校正沿用 model-switch 的 session continuity，不额外清空会话。
- 显式 `/model effort ...` 或 `/reasoning ...`：沿用现有 effort-switch 合同，清理旧 agent session ID/history，确保下一轮使用新强度。
- Role profile：`max / ultra` 不再被清空；role 自己声明的 model/effort 仍由 role 切换一次性应用。
- Catalog 不含 effort metadata：保持当前四项 fallback，不阻塞其他 agent/provider。

## Error Handling

- 非法或当前模型不支持的 effort 返回现有 reasoning usage/error，不修改 agent/session。
- model catalog 读取失败时回退 agent effort 列表。
- workspace resolution 失败沿用现有 `commandContext`/card navigation 错误处理。
- 不把 `max / ultra` 静默降级为 `xhigh`。

## Testing

按 TDD 增加以下回归：

- `agent/codex/codex_cache_test.go`：解析默认 effort 和 model-specific supported efforts。
- `agent/codex/session_test.go`：`max / ultra` 与拼写别名 normalization；CLI 参数保持 canonical value。
- `agent/codex/role_test.go`：role profile 的 `max / ultra` 不被清空。
- `core/engine_test.go`：Sol/Terra 显示六项、Luna 不显示 `ultra`、`/model effort` 切换、unsupported effort 拒绝、model switch 自动回退 default effort。
- workspace card regression：手机 action 只修改当前 workspace agent/session，不触碰 global state。

Focused verification：

```bash
go test ./agent/codex -count=1
go test ./core/ -run 'Test.*(Model|Reasoning)' -count=1
go test ./core/ -run TestCUJ -count=1
go test -p 1 ./...
go build -buildvcs=false ./...
```

真实部署 smoke：重启 `cc-connect.service`，确认飞书 websocket connected，并由手机打开 `/model` 验证 Sol/Terra 六档、Luna 五档及 workspace 隔离。
