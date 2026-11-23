# OKB v2 数据与存储模型

日期：2026-11-16。**v2.1 修订：2026-11-20**（配合 [requirements.md](requirements.md) v2.1：edge_kind 三态、severity 体系、node_kind、结构化 signature、source 枚举扩展——修订动机与对照见 requirements §11）。存储：独立 MySQL 知识库 + 文件/对象存储。Git 只保存代码、Schema 与合成样例，不保存真实工单正文、生产案例、凭据或模型输出。

## 1 不可变层（从 v1 继承，此部分设计是正确的）

| 表 | 职责 | 关键字段 |
| --- | --- | --- |
| `source` | 数据源登记。**v2.1 枚举扩**：`internal_case` / `vendor_doc` / `expert_input` / `alert_strategy`（策略库）/ `alert_event`（真实告警样本，原件入对象存储）/ `system_code`（既有系统固化规则）/ `industry_report`（公开行业报告）；含读取权限分级 | kind, title, sensitivity_cleared, permission_level |
| `source_version` | 原件版本，内容寻址，不可变 | source_id, content_hash, storage_uri |
| `parsed_version` | 解析版本，绑定固定原件版本，不可变 | source_version_id, parser_version, content_hash |
| `fragment` | 文本片段：字符区间/页码/段落路径 + 原文摘要，绑定固定解析版本 | parsed_version_id, char_range, page, summary, permission_level |

- 工单入库前做脱敏校验，通过后 source 打 `sensitivity_cleared`；未通过不入库（F4）。
- 权限分级字段（source/fragment 的 permission_level）保留在模型中，但**本期不启用**（2026-11-16 用户决定，后置 S3）；evidence 原文直返。

## 2 图谱层

| 表 | 职责 | 关键字段 |
| --- | --- | --- |
| `anomaly_type` | 图节点；`stable_code` 唯一键。**v2.1 增**：`domain`（server/network/dci）、`severity_online`（线上等级 P0-P4，源刻度 S/A/L 经等级映射归一）、`severity_dict`（字典等级 P1-P3）、`node_kind`（`atomic` 原子故障 / `aggregate` 多因汇总态——诊断需下游分解 / `placeholder` 监控空白预留位 / `event` 事件类）、`clock_hint`（处置时钟：`seconds`/`minutes`/`hours`，如液冷分钟级/风冷小时级） | stable_code, name, category, current_revision_id, domain, severity_online, severity_dict, node_kind, clock_hint |
| `anomaly_alias` | 厂商别名 → stable_code 的节点对齐表 | anomaly_id, alias, vendor, merged_by |
| `causal_rule` | 图边。**v2.1 增**：`edge_kind`（`causal` 因果可遍历 / `same_event` 同一物理事件两侧观测——双向语义单行存储 / `same_family` 机理同族对比）——`same_event`/`same_family` **不参与因果遍历**，以关联标注返回（语义见 api-boundaries） | source_id(anomaly), target_id(anomaly), **edge_kind**, conditions, time_constraints, evidence_level, confidence, current_revision_id, status |
| `alert_mapping` | 入口：告警 → 候选异常集合。**v2.1 增：signature 结构化**（四域四通道键形不同，单字符串装不下）：`domain` + `channel`（`agent_syslog` / `bmc_syslog` / `snmptrap` / `metric` / `dci_point` / `ufm` / `enms`）+ `sig_key`（正则/策略名/trap 名/指标+阈值/监控点路径/GUID 对/告警名+位置，按通道定形）+ `upgrade_ref`（升级链关联策略 id，同码归并）；`sig_key` 建规范索引，仍支持原单串 `alert_signature` 查询（拼接键兼容） | domain, channel, sig_key, upgrade_ref, anomaly_id, confidence, current_revision_id, status |

- `conditions` / `time_constraints` 用受 Schema 约束的 JSON，不执行任意脚本。
- 状态仅 `draft / curated` 两态；置信度由反馈飞轮回写。
- **v2.1 time_constraints 档位**（三域实证）：`seconds`（电力 trap 级联）/ `minutes`（液冷 CDU 供液温度）/ `hours`（风冷房间温度、蓄冷惯性）——传播延迟档供 trace 的"同时间窗"判定与诊断紧迫性排序使用；**告警到达顺序≠传播顺序**（消息路径不保证），窗口判定只依赖本字段。
- **v2.1 severity 冲突裁决**：多来源等级冲突按样本优先仲裁（requirements §4），裁决走新修订，原值留修订历史。

## 3 证据与修订层

| 表 | 职责 | 关键字段 |
| --- | --- | --- |
| `evidence` | 统一证据记录：种类（case/doc/inferred/expert）→ fragment 回链 → 核验状态 | kind, fragment_id, created_by(model/expert), verified |
| `revision` | 追加式修订历史：每次变更插入新行，不覆盖 | entity_type, entity_id, content_snapshot, prev_revision_id, changed_by, reason |
| `feedback` | 运行时反馈：Agent 上报的诊断使用的边与判定 | diagnosis_ref, edge_id, verdict(confirmed/rejected), operator_id, created_at |

- 印证计数：`causal_rule` 升 `curated` 需 ≥2 个独立工单的 evidence 支持；抽样审核比例默认 10%（F2）。
- 被否决（rejected）的边：置信度下降，自动降级并进入复核队列；否决记录本身作为反证入库。
- API 响应携带 `revision_id`，答案钉住修订版本（F5）。

## 4 物理形态

- 第一期：MySQL 关系表 + 服务内 BFS 遍历；几百节点/几千边规模足够。
- 未来：导出 NebulaGraph 加速查询——独立图空间，不碰 UnifiedInfraTopology 的 Schema 与图空间；索引可从固定数据重建，不成为第二权威。
- 对象存储：原件、解析大对象、模型原始输出；数据库存不可变引用与摘要。

## 5 分层职责

handler 不直接访问数据库或调用模型；service 组织业务流程（摄取/提取/查询/反馈）；repository 管数据一致性；document/extractor/storage 为可替换适配器。身份服务端识别，按动作（读取/编辑/提取/审核/反馈）授权，不信任客户端传入的操作者身份。
