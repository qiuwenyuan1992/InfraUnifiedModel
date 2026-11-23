# UnifiedInfraDiagnosis 协作与实施约束

本文件适用于 UnifiedInfraDiagnosis 及其子目录。用户当前明确指令优先；根目录 Agents.md 的中文要求继续适用。

## 模块定位

- 本模块是**诊断编排层**：基于 LangGraph 的确定性判定序执行器，消费 UnifiedInfraOKB 的 REST 四原语（lookup / trace / evidence / report）完成故障诊断。
- 与 OKB 的关系是**纯 REST 消费方**：不直连 OKB 数据库、不 import OKB 代码、不修改 OKB 任何文件；OKB 契约以 [api-boundaries.md](../UnifiedInfraOKB/docs/design/api-boundaries.md) 为准。
- 职责边界（OKB F1 的对偶）：**事故会话状态（当前假设、已取证、判定进度）归本层**，由 LangGraph checkpointer 持久化；故障知识归 OKB；资产拓扑影响归 UnifiedInfraTopology。

## 判定序资产

- 三查筛查、跨域聚合、根因仲裁等**判定序知识**归本模块 `graphs/`，以代码+测试固化；叙述版见 `UnifiedInfrastructureModel/跨域关联全景.md`。
- 判定序节点以确定性逻辑为主；LLM 仅在明确标注的节点介入（告警语义化/证据总结/结论生成），LLM 节点必须可被规则节点替代降级（无 LLM 配置时全流程仍可跑通）。

## 工程纪律（沿用仓库规矩）

- 测试先行：判定序的每条金路径（如机柜掉电）须有回归测试；真实失败在前，实现转绿在后。
- 不宣称未验证的结果：`uv run pytest` 实际退出码为准；图能跑 ≠ 诊断正确，诊断结论须附证据回链（edge_id + revision_id）。
- 真实告警样本、凭据、模型原始输出不进 Git；`checkpointer/` 本地会话数据（sqlite）不进 Git。
- Python 工具链：uv 管理（pyproject.toml + uv.lock）；Python ≥ 3.12。
- 未经当前任务明确授权不提交、推送、创建 PR 或合并。
