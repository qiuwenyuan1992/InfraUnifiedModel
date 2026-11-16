# UnifiedInfraOKB 存储模型

日期：2026-11-16。版本：v0.1。状态：逻辑设计，待 K01/K02 冻结；本文不是 DDL，也不表示数据库或对象存储已实现。

本设计以 [需求基线](requirements.md) 为依据，实施顺序见 [开发交接](../development/README.md)。本轮仅交付目录与文档。

## 1 权威边界与设计约定

- 独立 MySQL 知识数据库保存知识正文、修订、出处、审核及发布元数据；可与现有服务共用实例，但数据库、连接、权限、迁移台账和事务上下文独立。
- 原件、解析大对象、模型原始响应和发布包存放于文件或私有对象存储；数据库中的对象位置和摘要是受控引用，不提供第二个知识编辑入口。
- 不引用拓扑数据库、实际资产 ID、NebulaGraph、TopologyDetails 或拓扑控制表；资源类型只表达独立领域概念。
- 下列表名、字段、枚举、长度及索引均为 v0.1 建议；K01 冻结 API/字段字典，K02 冻结最终 DDL、迁移及数据库版本兼容策略。
- `PK` 为主键，`FK` 为外键，`UK` 为唯一约束；字段名省略的通用列见下文。关系不因省略 SQL 类型而允许悬空引用。
- API 的所有 ID 建议使用不透明字符串；内部采用字符、二进制或数值存储由 K02 决定，客户端不得解析前缀、顺序或数据库自增值。
- 时间统一 UTC，建议 `DATETIME(6)`；业务事件时间、采集时间、供应方版本时间分别记录，未知值用空值并说明原因。
- 可修改记录带 `row_version`；创建和更新记录带真实 `created_by`、`created_at`、`updated_by`、`updated_at`，主体引用 `actor`。
- 不可变记录只追加；状态迁移不等于允许重写正文。已被出处、审核、发布或审计引用的记录采用限制删除，不能级联物理删除。
- 所有内容摘要注明算法，建议 SHA-256；规范化 JSON、字段顺序、空值、字符串和数组规则随 `canonicalization_version` 固定。
- 条件、签名、定位与清单可用 MySQL JSON，但必须绑定版本化 JSON Schema，限制大小、深度、字段与操作符；不保存可执行 SQL、脚本或任意表达式。
- 外键能表达的约束落数据库；跨表类型、状态、JSON 引用和闭包约束由事务内服务校验补足，不能只靠前端。

## 2 身份与大对象

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `actor` | `actor_id PK`，`issuer`、`subject`、显示名快照、启停状态；`UK(issuer, subject)`。主体由服务端验证的身份映射产生，不复制口令或模型密钥。 |
| `blob_object` | `object_id PK`，`storage_backend`、`namespace`、`object_key`、`object_version`、`sha256`、`byte_length`、`media_type`、`purpose`、`state`、`verified_at`；`UK(backend, namespace, object_key, object_version)`。 |
| `object_reference` | `reference_id PK`，`object_id FK`、受控 `owner_kind`、`owner_id`、`role`、创建时间；`UK(owner_kind, owner_id, role, object_id)`。用作保留/回收账本，不能替代正文表的对象外键。 |

`blob_object.state` 建议为 `staging/available/quarantined/deletion_pending/deleted`；对象只有校验长度和摘要后才可被业务接受。
对象内容键建议包含摘要或不可重复的上传标识；同一键不可覆盖。下载地址为临时授权结果，不持久化含签名 URL。
可以按内容摘要复用对象，但不能据此合并资料身份、供应方版本或来源独立性。去重索引和保留周期由 K02 冻结。
文件系统后端也必须实现暂存、校验、不可变目标名与引用保留语义，不能依赖路径可读就判定提交成功。

## 3 来源、原件和解析版本

来源谱系与文件去重分别建模：相同字节可对应不同采集记录，不同网页也可能来自同一原始声明。

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `source` | `source_id PK`，来源类型、名称、发布方、官方地址、原始出处说明；表示来源身份，不承载知识正文。 |
| `source_assessment` | `assessment_id PK`，`source_id FK`、`assessment_no`、可访问性、内容有效性、独立性判断、判断依据、评估主体/时间；`UK(source_id, assessment_no)`，追加保存。 |
| `source_relation` | `relation_id PK`，`from_source_id FK`、`to_source_id FK`、`relation_kind`、依据、评估主体/时间、替代的旧关系 ID；关系包括转载、引用、共同原始来源、更正，禁止自引用。 |
| `document` | `document_id PK`，`source_id FK`、标题、规范地址、资料类型、逻辑识别键；`UK(source_id, logical_key)`，人工拆分/合并须留审计。 |
| `document_version` | `document_version_id PK`，`document_id FK`、`version_no`、标称版本、发布日期、`original_object_id FK`、`original_sha256`、媒体类型；`UK(document_id, version_no)`、`UK(document_id, original_sha256)`，原件不可变。 |
| `document_acquisition` | `acquisition_id PK`，`document_version_id FK`、采集地址、采集时间、声明的版本、导入主体、导入幂等键、去重结果；`UK(actor_id, import_key)`。 |
| `parse_version` | `parse_version_id PK`，`document_version_id FK`、`parse_no`、解析器/配置/Schema 版本、状态、错误码、输出 `object_id FK`、输出摘要、覆盖范围；`UK(document_version_id, parse_no)`。 |
| `source_fragment` | `fragment_id PK`，`parse_version_id FK`、片段序号、`locator_schema_version`、`locator_json`、摘录、摘录摘要；`UK(parse_version_id, fragment_no)`，完成的解析片段不可改写。 |

原件摘要相同的重复导入先返回既有版本并增加采集记录；是否同一资料身份由规范身份规则判断，不能全库按摘要直接归并资料。
同一字节却声明不同版本等冲突保存在采集记录并要求处理；新字节生成新原件版本，重新解析生成新解析版本。
解析失败、未支持和待人工处理须明确区分；失败记录不能带“已覆盖全文”的结论。重跑也保留旧解析版本及诊断。
PDF 定位可包含真实页号、段落、字符区间及坐标；HTML 使用章节路径、节点标识、段落和字符区间，不生成伪页号。
字符区间必须约定编码、起止边界及对应的规范化文本对象；摘录校验依赖确定的解析版本，不能回查当前网页替换历史证据。
来源可访问性和内容有效性分别记录，断网不等于证伪；更正/撤销产生新评估及影响事件，旧评估继续供历史发布回查。
独立依据计数使用经复核的谱系关系；转载、共同原始来源及重复片段归为同一依据组，未知独立性不得自动计为新增独立支持。
发布清单固定使用的评估和关系 ID；日后发现谱系错误应产生影响清单，不能悄悄重写旧发布中的独立性判断。

## 4 主题、缺口和定向补查

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `topic` | `topic_id PK`，稳定主题名、当前编辑版本、归档状态、`row_version`。主题与知识身份不同。 |
| `topic_revision` | `topic_revision_id PK`，`topic_id FK`、`revision_no`、整理目标、范围、问题列表、目标知识种类、覆盖说明、摘要；`UK(topic_id, revision_no)`，输入任务后冻结。 |
| `topic_document` | `topic_revision_id FK`、`document_version_id FK`、选定 `parse_version_id FK`、纳入范围、纳入理由；`PK(topic_revision_id, document_version_id, parse_version_id)`。 |
| `topic_gap` | `gap_id PK`，`topic_id FK`、稳定问题键、问题描述、目标字段/知识种类；`UK(topic_id, gap_key)`。 |
| `gap_observation` | `observation_id PK`，`gap_id FK`、`topic_revision_id FK`、状态、范围、理由、支持/反证摘要、`supersedes_id FK`、记录主体/时间；追加保存。 |
| `supplement_search` | `search_id PK`，`topic_revision_id FK`、`gap_id FK` 可空、检索问题、查询/筛选、检索工具配置版本、检索范围清单、时间、执行主体、结果状态及未找到理由。 |
| `search_result` | `result_id PK`，`search_id FK`、顺序号、命中地址/摘要、可空 `document_version_id FK`/`fragment_id FK`、采纳决定；`UK(search_id, ordinal)`。 |

关联的解析必须属于指定原件版本，gap 必须属于对应主题；建议以复合外键或事务内关联校验限制，不接受分别存在但彼此无关的 ID。
缺口状态建议为 `not_searched/not_found/insufficient/supported/conflicting/negated/not_applicable`，其语义由 K01 固定。
“已检索未找到”必须引用可回查的补查记录；没有模型、检索失败或只查看 Top-K 不能自动转成 `not_found` 或 `supported`。
主题覆盖与知识审核分别计算；多个缺口可关联多个修订，关联表 `revision_gap(revision_id FK, observation_id FK)` 以两者为复合主键。
知识候选原因列表须另存 `completeness=unknown/partial/bounded_complete` 及范围依据；不得用主题状态暗示集合已穷尽。
补资料只新增来源与版本或专家经验记录，不回写旧任务输入。发布固定选用的缺口观察，便于说明部分知识的可用用途。

## 5 提取任务、执行尝试与候选

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `extraction_job` | `job_id PK`，`topic_revision_id FK`、任务类型、模型配置标识、提示词/Schema 版本、输入清单摘要、状态、`active_attempt_no`、`execution_generation`、取消时间、`row_version`、创建幂等键。 |
| `job_document_input` | `job_id FK`、`parse_version_id FK`、范围及覆盖要求；`PK(job_id, parse_version_id)`。原件版本经解析外键确定，不能只传“最新资料”。 |
| `job_revision_context` | `job_id FK`、`revision_id FK`、上下文角色及摘要；`PK(job_id, revision_id, role)`，固定已有知识上下文。 |
| `job_attempt` | `attempt_id PK`，`job_id FK`、`attempt_no`、执行代次、worker、租约标识/截止时间、开始/结束时间、结果状态、原始响应 `object_id FK`、解析校验报告、错误码；`UK(job_id, attempt_no)`。 |
| `extraction_candidate` | `candidate_id PK`，`topic_revision_id FK`、`attempt_id FK` 可空、`candidate_key`、`candidate_kind`、`knowledge_kind` 可空、原始/工作 JSON、Schema 版本、原始/工作摘要、状态、`row_version`、创建主体；`UK(attempt_id, candidate_key)`。 |
| `candidate_dependency` | `candidate_id FK`、`premise_candidate_id FK`、固定前提摘要、关系类型及推导说明；`PK(candidate_id, premise_candidate_id, relation_kind)`，不能自依赖。 |

任务状态统一建议为 `queued/running/succeeded/failed/canceled`；候选状态为 `pending/accepted/rejected`。尝试有自己的完成结果，不能覆盖任务终态。
领取、续租和结果提交均检查 `execution_generation + attempt_no + lease_token`；取消/重试先增加执行代次，晚到结果仅作历史记录。
一次尝试中候选及校验结果在同一事务中登记；Schema 失败的响应保留为诊断，不登记可接纳候选。模型任务结束后释放租约，不等待专家审核。
输入清单包含确定的主题、解析、范围和上下文修订；原始响应和配置版本可回查，模型密钥不写入清单、表字段或日志。
`candidate_kind` 仅为 `assertion/knowledge_draft`：前者是一条原子陈述及范围，后者必须有三类正式 `knowledge_kind` 之一。
原子陈述不建立第四类正式知识表；其接纳表示成为组装材料，不能直接生成 `knowledge_revision` 或进入发布包。
组装草稿引用一个或多个断言的固定摘要；推导说明、冲突和缺失条件保留，A→B 与 B→C 不能自动得到已审核的 A→C。
模型原始 JSON 不变；专家工作 JSON 修改使用版本检查并记差异审计。接纳后工作摘要冻结，再改动须创建新的派生候选。
人工候选的 `attempt_id` 为空，记录真实主体与创建原因；模型候选的主题版本必须与任务相同，不能伪造一次模型执行以满足外键。

## 6 知识身份、修订与三类正文

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `knowledge_item` | `knowledge_id PK`，不可变 `kind`、稳定编码、归档状态、当前草稿提示；`UK(kind, stable_code)`。稳定 ID 不等于当前修订。 |
| `knowledge_revision` | `revision_id PK`，`knowledge_id FK`、`revision_no`、`parent_revision_id FK`、标题、适用条件、Schema 版本、状态、允许用途、变更理由、正文摘要、`review_hash`、`row_version`；`UK(knowledge_id, revision_no)`。 |
| `anomaly_type_content` | `revision_id PK/FK`，定义、资源概念列表、典型表现、判别条件、排除条件及必要说明；字段采用标量或受 Schema 约束 JSON。 |
| `alert_mapping_content` | `revision_id PK/FK`，厂商、产品、版本约束、事件码/告警规则、`signature_json`、候选集合完整性及已知缺口。 |
| `alert_mapping_option` | `option_id PK`，`mapping_revision_id FK`、局部稳定键、`anomaly_type_revision_id FK`、适用/排除条件、依据说明、排序；`UK(mapping_revision_id, option_key)`。 |
| `causal_rule_content` | `revision_id PK/FK`，源/目标 `anomaly_type_revision_id FK`、可能机制、条件、时间约束、支持/反证要求、未知处理、准入判断及理由。 |

正式 `kind` 固定为 `anomaly_type/alert_mapping/causal_rule`。每个修订恰有一份与其 item.kind 相符的正文，不能混写或跨 kind 修改 item。
映射可有多个候选异常，同一异常在不同条件下可出现多次；唯一键不能错误限制为“映射+异常”而丢失条件差异。
传播规则表达条件下的可能性；不默认因果必然、概率或反向成立。缺失必要条件的规则可保留为参考，不能靠默认值补成可判定规则。
`allowed_uses` 为受 Schema 约束集合，v0.1 建议 `reference/expert_assistance`；本期不包含自动判断或设备执行用途。
修订状态统一建议为 `draft/in_review/approved/rejected`。draft 可按并发版本编辑；提交审核后正文、出处、依赖及用途冻结。
退回为 `rejected`，再次编辑从冻结版本派生新的 draft 修订；不能把原修订改回 draft 后覆盖已审核的内容。
父修订必须属于同一知识身份；跨知识合并用显式沿革关系，不滥用 parent。作废/替代关系与历史审核决定分别记录。
正式引用始终使用具体 `revision_id`；列表中的“当前草稿”提示或检索排序不参与规则依赖、审核或发布解析。

## 7 字段出处、专家经验与来源独立性

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `expert_statement` | `statement_id PK`，经验陈述、适用范围、形成背景、限制、真实主体、时间、摘要；追加保存，修正生成新记录。经验不伪装为文档摘录。 |
| `field_provenance` | `provenance_id PK`，`revision_id FK` 或 `candidate_id FK`、所属正文摘要、字段 JSON Pointer、`assertion_candidate_id FK` 可空、证据种类、支持/反驳/上下文、核验状态、说明。 |
| `provenance_basis` | `provenance_id FK`、`basis_key`、`fragment_id FK`/`statement_id FK`/`premise_revision_id FK`、引用摘要或定位子范围；`PK(provenance_id, basis_key)`，每行恰选一种依据。 |
| `revision_source_snapshot` | `revision_id FK`、`source_id FK`、`assessment_id FK`、本修订内的依据组键及判断说明；`PK(revision_id, source_id)`，提交审核时冻结。 |
| `revision_source_relation` | `revision_id FK`、`relation_id FK`；`PK(revision_id, relation_id)`，固定审核使用的谱系关系，不读取来源的最新关系替代。 |

`field_provenance` 恰有一个所属候选或修订；字段路径必须存在，数组项使用稳定 option/key，不能仅靠会随排序变化的位置号。
证据种类区分 `explicit/inferred/expert_experience`；生产方式由主体/提取批次另记，不能把“模型创建”当作“无依据”，或把“专家创建”当作“已验证”。
原文明示至少有片段依据；专家经验至少有经验记录；推导至少有固定前提及推导说明，不能只留一句模型结论。
引用的断言必须是 `candidate_kind=assertion`，且固定其接纳摘要；断言只提供组装沿革，不能替代直接保留片段/经验/前提修订引用。
同一字段可有多条支持及反证；一个片段可支持多个字段，但要分别核验摘录、范围与字段语义，不按关联行数累计独立来源。
候选改动导致正文摘要变化后，旧出处仍保留但不可自动作为新正文的已核验出处；接纳时复制/绑定经确认的出处到修订。
提交审核必须校验片段属于输入解析、摘录摘要吻合、引用存在及来源谱系；未核验或冲突可按用途显式保留，不能静默提升可信度。
文档/解析/片段外键形成证据路径；来源快照补充独立性判断，两条路径缺一不可。来源失效/更正沿这两条路径计算影响修订和发布。

## 8 修订依赖与候选拆合

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `revision_dependency` | `revision_id FK`、`depends_on_revision_id FK`、依赖角色、字段路径、被依赖摘要；`PK(revision_id, role, field_path, depends_on_revision_id)`，禁止自依赖。 |
| `candidate_operation` | `operation_id PK`，操作类型、原因、`actor_id FK`、幂等键、请求摘要、操作时间；`UK(actor_id, idempotency_key)`。 |
| `candidate_operation_input` | `operation_id FK`、`candidate_id FK`、固定候选工作摘要；`PK(operation_id, candidate_id)`。 |
| `candidate_operation_output` | `operation_id FK`、输出序号、`candidate_id FK` 或 `revision_id FK`；`PK(operation_id, ordinal)`，每行恰选一种输出。 |
| `knowledge_relation` | `relation_id PK`，源/目标 `knowledge_id FK`、关系种类、原因、操作主体/时间；用于替代、拆分、合并沿革，不改变已有修订的语义。 |

正文中的异常引用必须与 `revision_dependency` 一致；字段内 JSON 不能藏有未登记依赖。异常引用角色的依赖目标 kind 必须是 `anomaly_type`。
其他推导依赖也固定具体修订并纳入闭包；拒绝成环的推导依赖，不能用环证明自身。闭包内同一 knowledge_id 出现不同修订应阻止发布。
拆分可一入多出，合并可多入一出；多候选也可生成多个知识草稿修订，操作输入/输出连接保留完整多对多沿革。
断言接纳可无修订输出；组装操作输出 `knowledge_draft` 候选。只有具备明确 knowledge_kind 的知识草稿接纳才允许输出正式 draft 修订。
接纳在一个数据库事务内检查候选状态/摘要、幂等键、创建修订、复制出处及依赖并登记操作；同键同请求返回原结果，同键异请求返回冲突。
驳回必须记理由及固定摘要；不删除候选或模型原始输出。已接纳候选不能再次原地改写后沿用同一接纳结果。

## 9 审核冻结、并发与审计

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `review_submission` | `submission_id PK`，`revision_id FK`、`review_hash`、规范化版本、来源/依赖/缺口快照摘要、提交主体/时间；每个冻结修订最多一次有效提交。 |
| `review_decision` | `decision_id PK`，`submission_id FK`、`revision_id FK`、`review_hash`、决定、理由、真实审核主体/时间；有效最终决定唯一性由 K02 落约束。 |
| `action_audit` | `audit_id PK`，请求/关联 ID、`actor_id FK`、动作、对象种类/ID、前后摘要/版本、理由、结果及 UTC 时间；追加保存。 |

保存草稿用 `WHERE revision_id=? AND row_version=? AND status='draft'` 类条件更新并递增版本；零行受影响返回明确冲突。
`review_hash` 覆盖正文、三类子表、字段出处、固定依赖、来源独立性快照、允许用途、已知缺口及 Schema/规范化版本，不仅覆盖标题与内容 JSON。
提交在事务内读取一致快照、校验引用并冻结；批准再次比对 revision_id、submission_id 和 review_hash，禁止审批旧摘要或另一个修订。
身份和动作权限由服务端验证；同一人可具多项权限，不预设双人审核。审计不记录凭据、授权头或未经脱敏的完整模型配置。
对不可变记录的更正采用派生/替代记录；审计记录不得作为恢复出已被覆盖原文的唯一手段。

## 10 固定发布、闭包和对象提交

| 逻辑表 | 关键字段及约束 |
| --- | --- |
| `knowledge_release` | `release_id PK`，`release_label UK`、状态、Schema/规范化版本、计划摘要、`manifest_object_id FK`、manifest/包摘要、构建代次、构建/发布时间、创建主体、失败原因、`row_version`。 |
| `release_member` | `release_id FK`、`knowledge_id FK`、`revision_id FK`、修订/review 摘要；`PK(release_id, knowledge_id)`，并设 `UK(release_id, revision_id)`。 |
| `release_source` | `release_id FK`、`source_entry_key`、`entry_kind`、摘要，及 `document_version_id/parse_version_id/fragment_id/assessment_id/relation_id/statement_id` 各自 FK；`PK(release_id, source_entry_key)`，每行按 kind 恰选一个依据外键。 |
| `release_validation` | `validation_id PK`，`release_id FK`、计划摘要、校验器/评测版本、结论、报告 `object_id FK`、已知缺口快照、执行时间；只接纳与当前计划摘要一致的通过结果。 |
| `release_channel` | `channel_id PK`，`active_release_id FK` 可空、`row_version`；v0.1 建议单一正式通道，不引入租户或拓扑作用域。 |
| `release_event` | `event_id PK`，`release_id FK`、事件种类、前/后启用版本、期望通道版本、主体、理由、影响清单/校验引用、时间；撤回及回退都追加事件。 |

发布状态统一建议为 `building/published/failed/withdrawn`；`published` 表示包已固定，是否启用只看 `release_channel`。
manifest 固定成员及摘要、完整依赖边、来源版本/定位与独立性快照、Schema、校验器、评测结论、用途和已知缺口；稳定排序后计算摘要。
发布闭包从明确选择的已批准修订出发递归展开；所有依赖必须已批准且属于同一计划，不自动替换成最新异常定义。
发布成员主键阻止同包双版本；在写包前再核对三类正文、出处、已保存引用对象的可读性/完整性、来源有效性评估、审核摘要、依赖闭包及用途准入。原站 URL 暂时不可访问不自动否定已校验快照，也不等于内容被证伪。
引用的原件和解析对象保留受控访问与摘要，不要求把受限原文复制进可下载发布包；导出授权与原件保留策略分别执行。

对象存储与数据库不能原子提交，建议采用以下可恢复协议：

1. 数据库事务创建 building 记录、固定成员/来源/缺口与计划摘要；构建领取带执行代次，冻结后更换计划须新建 release。
2. 在事务外写暂存或内容寻址对象；生成 manifest 和包，校验摘要、长度、可读性及完整清单后将对象标记 available。
3. 数据库事务锁定 release，检查仍为 building、构建代次/计划摘要相符、校验通过和对象 available；绑定摘要/对象并改为 published。
4. 启用可同事务或独立事务执行：比较 release_channel.row_version，检查目标仍可用且未撤回，切换指针并追加事件；冲突不覆盖他人切换。
5. 文件失败不切换指针；写成功而数据库失败的对象等待重试复用或延迟回收；数据库不得提前公布未校验对象。

回退是启用经当前检查可用的历史 published 包，不重建正文；历史包的内容和 manifest 不因回退改变。
撤回先登记原因及受影响依赖，在事务内改为 withdrawn；若当前启用，清空指针或切换至已校验替代包，记录实际结果。
withdrawn 包不能直接重新激活；需修正后构建新包。历史包遇到来源更正、证伪或失效，也必须重新评估后才允许回退启用。
删除任务先检查所有业务外键、对象引用账本、发布和审计保留，再延迟回收孤立暂存对象；发布依赖对象禁止按文件年龄清理。
正式查询携带并固定 active release_id 与 manifest 摘要；检索索引由该包重建，索引版本不符时不能混入其他发布或草稿。

## 11 K01/K02 冻结清单

- K01：ID/时间/枚举字典、JSON Schema、字段路径规则、身份主体协议、幂等范围、摘要规范、用途准入、错误码及 API 并发契约。
- K02：MySQL 版本、物理字段/长度/字符集、复合外键、CHECK 可用性、唯一索引、锁与隔离级别、事务重试、对象保留/回收及备份恢复边界。
- 优先核定不可变内容与状态列的更新权限、候选输出类型约束、同包单修订、来源关系版本化和发布指针竞争；实现不能以 JSON 灵活性绕过这些约束。
- 所有样例使用合成资料；不复制真实资产、内部文档、生产连接或凭据。本文未执行 DDL、应用初始化、模型调用或运行验收。
