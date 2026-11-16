# 协作约定

## 语言

- 默认使用中文回复用户。
- 进度说明、问题确认、设计讨论和最终总结均使用中文。
- 代码标识符、文件路径、命令及专有名词保持原样。
- 除非用户明确要求，否则不要切换为其他语言。

## 运维知识模块入口

- UnifiedInfraOKB 是独立知识管理模块，先读 [模块协作约束](UnifiedInfraOKB/AGENTS.md) 与 [接手入口](UnifiedInfraOKB/docs/development/README.md)。当前只有骨架与设计，不代表功能已实现。
- 当前知识管理不关联实际拓扑，不使用或修改 TopologyDetails.vue；不要将 UnifiedInfraTopology 的存储与模型约束直接当成 OKB 数据结构。
- 前端接入由明确的 K07 工作包负责；各任务遵守允许路径、契约版本和交付记录要求。
