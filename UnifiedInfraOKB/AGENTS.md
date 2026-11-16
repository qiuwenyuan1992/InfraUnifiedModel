# UnifiedInfraOKB 协作与实施约束（v2）

本文件适用于 UnifiedInfraOKB 及其子目录。用户当前明确指令优先；根目录 Agents.md 的中文要求继续适用。

v2 基线（2026-11-16）取代 v1 全部设计；v1 旧文档已整体删除，如遇残留旧设计描述一律以 v2 基线为准。

## 接手顺序

1. README.md（模块定位）
2. docs/design/requirements.md（v2 需求基线：定位、证据分级、准确性机制、切片 S0—S3、验收 A1—A7）
3. docs/design/storage-model.md（数据模型）
4. docs/design/api-boundaries.md（REST 四原语契约）
5. docs/development/README.md（切片派发与验证）

当前状态：S0 建馆 + S1 查询切片已实现（go.mod、模型、仓储、摄取、提取、四原语、测试见 docs/development/handoffs/2026-11-16-s0-s1-vertical-slice.md）；真实 LLM 提取器、S2 界面、迁移 DDL 尚未实现，不得宣称已验证真实模型或生产部署。

## 产品边界

- OKB 是**故障知识图谱图书馆**：结构化保存带证据出处的因果知识，供诊断 Agent（REST）与专家（极简界面）消费。复现目标：XiHe（SIGCOMM 2026，DOI 10.1145/3789240.3829145）的因果图与共享记忆资产。
- **OKB 不做诊断编排**：会话级工作记忆（事故状态、假设、已取证）归未来编排层；OKB 只被动应答 lookup / trace / evidence / report 四原语，不保存会话状态。
- 核心资产是图：anomaly_type（节点）、causal_rule（带条件/时间约束/证据等级的边）、alert_mapping（告警入口）。
- 数据源以脱敏工单/复盘（internal_case）为一等公民；厂商文档与专家经验为补充源。
- 不使用或修改 TopologyDetails.vue；不访问 CMDB/拓扑接口，不修改 UnifiedInfraTopology 的数据库、迁移、NebulaGraph Schema 及现有模型。
- 知识概念（异常类型）不是具体资产；不增加租户、多拓扑、作用域兼容层。

## 证据与准确性纪律

- 证据分级生来自源：case_verified / doc_explicit / inferred / expert_only；等级不事后提升，升级只能通过新证据 + 新修订。
- AI 输出永远是 draft 候选；升 curated 依赖：案例边抽样审核（默认 10%）+ ≥2 独立工单印证，非案例边须专家确认。
- 运行时反馈：report 上报确认/否决；否决自动降级入复核队列，反例同样入库作为反证。
- 禁止为补模板捏造事实、出处或概率；"谁创建"与"依据是什么"分别记录。
- 原文片段出处回链 + 固定版本解析是可解释性的根基，不可省略。
- stable_code 节点对齐：AI 出归并建议、人确认，不自动合并。

## 代码与存储边界

- 独立 Go module，独立 server/worker 入口；go.mod 已初始化（S0），worker 仍占位。
- 独立 MySQL 知识库：独立连接、迁移台账、事务上下文；不把知识写入拓扑控制库。
- 文件/对象存储保存原件、解析大对象、模型原始输出；数据库保存不可变引用、摘要和状态。
- Git 保存代码、Schema、设计和合成样例；不保存真实工单正文、生产案例、凭据、模型输出或运行数据。
- 工单入库前须过脱敏校验（sensitivity_cleared）；fragment 级权限分级**本期不启用**（2026-11-16 用户决定，后置 S3），evidence 原文直返。
- 修订历史追加式（每次变更插入新行）；知识状态仅 draft / curated 两态。
- 所有读 API 响应携带 revision_id，答案钉住修订版本。
- 可变条件用受 Schema 约束的 JSON，不执行任意脚本。
- 向量库、OCR、NebulaGraph 导出、MCP 包装不是本期必装依赖；需要时按 S3 按需立项。
- 模型服务通过适配器接入；开发验收可用明确标识的假模型，不声称已验证真实服务。

## 身份与信息边界

服务端识别真实操作主体，按动作（读取/编辑/提取/审核/反馈）分别授权，不信任客户端传入的操作者身份。测试身份不能成为生产默认值。

不得自动连接生产数据库、实际模型或设备；不得复制内部 URL、真实资产、原始工单内容或凭据到仓库。使用用户授权的环境与已脱敏的数据。

## 协作与交付

- 交付按切片：S0 建馆（摄取+提取+图存储）→ S1 查询（REST 四原语）→ S2 治理闭环（反馈回写+最小界面）→ S3 按需扩展（含权限分级）。先建馆后查询，S0 完成前不启动 S1。
- 每项任务记录实际分支、基线、允许路径与验收（对应 A1—A7）；并行任务使用独立 checkout/worktree，不共享未提交状态。
- 前端修改仅在 S2 范围内按明确清单修改 UnifiedInfraWeb；不得修改 UnifiedInfraTopology 相关代码。
- 实现采用真实失败测试先行；重点验证脱敏校验、证据回链、印证计数、反馈幂等与修订不可覆盖。
- 只有实际初始化后才能给出 go test/build 等运行证据；文档中的命令不代表通过结果。
- 未经当前任务明确授权不提交、推送、创建 PR 或合并。禁止强推、覆盖他人改动或跳过 hooks。
- 交付使用 docs/development/templates/handoff.md，说明实际实现、验证、未验证项、缺口及下一步。
