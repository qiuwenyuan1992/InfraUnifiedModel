# 给另一个 AI 的 UnifiedInfraOKB 接手提示词

将下方说明交给能访问仓库的 AI，并填写任务编号、实际基线及授权。没有被分配任务时先阅读并反馈，不默认实现整个系统。

## 可复制说明

你正在 qiuwenyuan1992/InfraUnifiedModel 仓库工作，负责独立 UnifiedInfraOKB 模块。面向用户用中文沟通。

首先检查实际分支、HEAD、工作区及权限，保留所有非本任务改动。本套材料的交付分支是 docs/okb-foundation，请核对其远程实际提交或使用派发者固定的提交。下面的源码核查基线 9ffbe77635112e03a5430acbd02e829566f932de 只描述原有仓库；它不包含新 OKB 文档，不能直接 checkout 旧基线后声称已拿到 OKB。

必读顺序：

1. 根 Agents.md、UnifiedInfraOKB/AGENTS.md、UnifiedInfraOKB/README.md。
2. UnifiedInfraOKB/docs/design/requirements.md。
3. UnifiedInfraOKB/docs/design/storage-model.md。
4. UnifiedInfraOKB/docs/design/workflows-and-ui.md。
5. UnifiedInfraOKB/docs/design/api-boundaries.md。
6. UnifiedInfraOKB/docs/development/README.md、work-packages.md、plans/2026-11-16-okb-implementation.md。
7. 所领取包的任务简报与前置包实际交付。

当前状态只有目录骨架和设计，没有 go.mod、运行服务、DDL、正式 OpenAPI、真实模型调用或知识前端。不要把未来命令、目录、mock 或固定模型描述为已实现生产能力。

已确认需求：

- 运维专家网页维护；同仓库独立 Go 后端，前端复用 UnifiedInfraWeb。
- 独立 MySQL 知识库是正文/修订/审核的唯一权威；文件/私有对象存储保存原件与大对象。
- 不使用 TopologyDetails.vue，不访问 CMDB 或拓扑，不修改 UnifiedInfraTopology、NebulaGraph 和拓扑控制库。
- 三种正式知识 kind 为 anomaly_type、alert_mapping、causal_rule。资源类型是知识概念，不是具体资产。
- 文档原件版本、解析版本、原文片段、提取任务及尝试分别记录。
- 单篇资料只提供有依据的陈述；按主题组合多份资料，保留缺口、冲突、版本差异、独立来源谱系及补查记录。
- candidate_kind=assertion 的内容只是组装材料；knowledge_draft 候选需明确三种 kind 之一，接纳只产生草稿。
- 原文明示、推导/专家经验、知识创建者与审核结论分别记录。重要字段可追到原文片段；候选不自动成为事实。
- 草稿并发检查，提交审核后内容/出处/依赖冻结；映射和规则引用确定异常修订。
- 发布包固定修订、依赖闭包、来源和摘要。先准备文件再切数据库启用指针；回退与撤回分别处理。
- 真实操作主体及按动作授权；生产不接受客户端伪造审核人。真实模型仅按获准配置接入，固定响应可用于离线验收。
- 结构化提取、资料检索与正式查询分开；OpenSPG/KAG、向量库、OCR、实际拓扑都不是第一期必装能力。

本次领取：

- 工作包/子任务：填写 K01—K08 中的一个。
- 基线和输入契约：由派发者填写。
- 允许修改路径：从对应工作包抄写并进一步缩小。
- 共享变更请求：K01 负责契约，K02 负责公共依赖/装配/迁移，K07 负责 Web。
- 验收：填写对应 A 编号、真实输入和通过标准。
- 提交/推送/PR/合并、真实数据库/模型/内部资料权限：由派发者明确填写，未授权不执行。

先确认前置接口和验证证据，按真实风险写失败测试再实现。没有必要的真实配置时使用显式测试适配并说明限制，不访问当前生产作为回放替代。依赖版本和工具选择由责任包核定并记录，不要求为了使用本提示词安装特定 AI 插件或技能。

结束时按 UnifiedInfraOKB/docs/development/templates/handoff.md 记录实际改动、运行结果、未测和未实现项、迁移影响及下一步。仅交付本任务范围，不能顺手实现其他包或重写拓扑/前端。
