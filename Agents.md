# 协作约定

## 语言

- 默认使用中文回复用户。
- 进度说明、问题确认、设计讨论和最终总结均使用中文。
- 代码标识符、文件路径、命令及专有名词保持原样。
- 除非用户明确要求，否则不要切换为其他语言。

## 故障知识图谱模块入口

- UnifiedInfraOKB 是独立的故障知识图谱与 Agent 诊断记忆底座（v2 基线）：脱敏工单一等数据源、带证据出处的因果图、REST 四原语（lookup/trace/evidence/report）消费。先读 [模块协作约束](UnifiedInfraOKB/AGENTS.md) 与 [接手入口](UnifiedInfraOKB/docs/development/README.md)。当前只有骨架与设计，不代表功能已实现。
- v1 知识管理设计已被 v2 基线整体取代，旧设计文档已删除；如遇残留旧设计描述一律以 v2 基线为准。
- 知识图谱不关联实际拓扑，不使用或修改 TopologyDetails.vue；不要将 UnifiedInfraTopology 的存储与模型约束直接当成 OKB 数据结构。
- 真实工单正文、凭据与模型输出不进 Git；工单入库前须过脱敏校验。前端接入仅在 S2 切片范围内按明确清单修改 UnifiedInfraWeb。
