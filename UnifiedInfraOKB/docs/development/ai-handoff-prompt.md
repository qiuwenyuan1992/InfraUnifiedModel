# 给另一个 AI 的 UnifiedInfraOKB 接手提示词（v2）

将下方说明交给能访问仓库的 AI，并填写任务编号、实际基线及授权。没有被分配任务时先阅读并反馈，不默认实现整个系统。

## 可复制说明

你正在 qiuwenyuan1992/InfraUnifiedModel 仓库工作，负责独立 UnifiedInfraOKB 模块。面向用户用中文沟通。

首先检查实际分支、HEAD、工作区及权限，保留所有非本任务改动。OKB 交付分支是 docs/okb-foundation，请核对其远程实际提交或使用派发者固定的提交。源码核查基线 9ffbe77635112e03a5430acbd02e829566f932de 只描述原有仓库，不包含 OKB 文档。

必读顺序：

1. 根 Agents.md、UnifiedInfraOKB/AGENTS.md、UnifiedInfraOKB/README.md。
2. UnifiedInfraOKB/docs/design/requirements.md（v2 需求基线，验收 A1—A7）。
3. UnifiedInfraOKB/docs/design/storage-model.md（数据模型：不可变层/图谱层/证据修订层）。
4. UnifiedInfraOKB/docs/design/api-boundaries.md（REST 四原语）。
5. UnifiedInfraOKB/docs/development/README.md（切片派发与验证要求）。
6. 所领切片的任务简报与前置交付记录（handoffs/）。

当前状态只有目录骨架和 v2 设计文档，没有 go.mod、运行服务、DDL、REST API、真实模型调用或前端。不要把未来命令、目录、mock 或固定模型描述为已实现生产能力。

已确认的 v2 需求要点：

- OKB 是故障知识图谱图书馆：anomaly_type（节点）、causal_rule（带条件/时间约束/证据等级的边）、alert_mapping（告警入口）。供诊断 Agent（REST）与专家（极简界面）消费。
- OKB 不做诊断编排、不保存会话状态；只被动应答 lookup / trace / evidence / report 四原语。
- 数据源以脱敏故障工单/复盘为一等公民（结构化程度低，须 LLM 二次结构化提取，输出为 draft 候选）；厂商文档与专家经验为补充源。
- 证据分级：case_verified / doc_explicit / inferred / expert_only；生来自源、不事后提升。
- 准确性三层：来源定级 → 抽样审核（默认 10%）+ ≥2 独立工单印证升 curated → 运行时反馈飞轮（report 确认/否决回写置信度，否决入库为反证）。
- 知识状态仅 draft / curated 两态；修订历史追加式；所有读 API 响应携带 revision_id。
- 工单入库前须过脱敏校验（sensitivity_cleared）；权限校验到 fragment 级，未授权只返回摘要。
- 不使用 TopologyDetails.vue，不访问 CMDB/拓扑，不修改 UnifiedInfraTopology、NebulaGraph 和拓扑控制库。
- REST 即可，本期不做 MCP；向量库、OCR、NebulaGraph 导出不是本期必装依赖。
- 交付切片：S0 建馆 → S1 查询 → S2 治理闭环 → S3 按需；先建馆后查询。
- v1 旧设计文档已删除；如遇残留旧设计描述一律以 v2 基线为准。

本次领取：

- 切片/子任务：填写 S0—S3 中的一个及子范围。
- 基线和输入契约：由派发者填写。
- 允许修改路径：从切片说明抄写并进一步缩小；前端修改仅在 S2 且按明确清单。
- 验收：填写对应 A 编号、真实输入和通过标准。
- 提交/推送/PR/合并、真实数据库/模型/工单数据权限：由派发者明确填写，未授权不执行。

先确认前置接口和验证证据，按真实风险写失败测试再实现。没有必要的真实配置时使用显式测试适配并说明局限，不访问当前生产作为回放替代。

结束时按 UnifiedInfraOKB/docs/development/templates/handoff.md 记录实际改动、运行结果、未测和未实现项、迁移影响及下一步。仅交付本任务范围，不能顺手实现其他切片或重写拓扑/前端。
