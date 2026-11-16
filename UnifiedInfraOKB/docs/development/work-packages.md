# 工作包与并行边界

日期：2026-11-16。以下均为待实现工作，当前没有已冻结的 API 或可运行的 OKB 服务。
阅读 [接手入口](README.md) 后，用 [任务简报](templates/task-brief.md) 派发；完整时序见 [planned 实施计划](plans/2026-11-16-okb-implementation.md)。
路径相对仓库根目录，下文 `OKB/` 指 `UnifiedInfraOKB/`。每包可另写自己的 `OKB/docs/development/handoffs/<任务编号>-<主题>.md`。
未列出的共享文件只能向维护者提交变更需求；迁移统一由 K02 落盘，契约统一由 K01 维护。本文不授予连接生产或上传内部资料的权限。

## K01 契约、Schema 与合成验收包

- 前置：需求基线、[数据设计](../design/storage-model.md)、[流程设计](../design/workflows-and-ui.md)；无需运行服务。
- 允许写入：`OKB/api/v1/**`、`OKB/internal/model/**`、`OKB/test/fixtures/synthetic/**`；设计文档变更交集成负责人同步。
- 产出：可版本化的 HTTP/OpenAPI 与知识 Schema、字段字典、错误码、状态机、ID/摘要/并发/幂等规则、发布 manifest、服务端接口约定及合成输入/预期结果。
- 明确资料限制、来源谱系、片段定位、提取执行代次、候选多对多接纳、冻结审核、用途/缺口、身份动作权限和发布依赖闭包；不以模型评分代替准入。
- 生产接口：供 K02—K07 共用的请求/响应、领域值、存储与身份/原件接口语义；消费接口：只消费需求，不自行绑定未选定外部产品。
- 契约门禁：与各生产者/消费者确认版本和样例后才形成首个稳定版本；不能把本轮设计叙述称作已冻结 API。
- A 覆盖：A01—A14 的字段/错误/状态及样例映射，重点 A04、A05、A07—A10、A12—A14 的约束。
- 独立验收：正反例能被 Schema/契约校验；伪造片段、缺失引用、重复知识修订、过期摘要和非法状态有明确预期；记录真实执行方式及结果。
- 交付：契约版本、未决决策、兼容策略、每个 A 的合成用例及消费者清单；无服务时只声明契约验收。

## K02 Go 基础、独立存储、身份授权与原件存储接口

- 前置：K01 首版稳定契约，获准的开发/测试身份、MySQL 和存储配置；无需真实模型。
- 允许写入：`OKB/go.mod`、`OKB/go.sum`、`OKB/cmd/**`、`OKB/config/**`、`OKB/internal/authorization/**`、`OKB/internal/storage/**`、`OKB/internal/repository/database/**`。
- 另可写入：`OKB/internal/service/runtime/**`、`OKB/internal/handler/runtime/**`、`OKB/internal/handler/router.go`、`OKB/internal/worker/runtime/**`、`OKB/migrations/mysql/**`、`OKB/test/integration/foundation/**`。
- 产出：独立 Go 模块和 server/worker 启动骨架、配置校验、数据库连接/事务、迁移台账、错误与审计基础、真实主体适配及按动作授权、不可变对象读写接口与测试实现。
- 生产接口：事务/仓储基础、鉴权上下文、授权检查、对象定位/摘要/读写、worker 装配与公共路由；消费 K01 契约和部署配置。
- 集成责任：管理依赖锁定、启动和路由注册；接收 K03—K06 的迁移需求并落盘。禁止领域包各建数据库或绕过统一身份。
- A 覆盖：A01 的不可变原件基础、A07 的并发基础、A08 的执行基础、A09 的存储失败基础、A11、A12、A14 的隔离基础。
- 独立验收：全新独立 MySQL 可迁移并保留台账；重启后数据可读；未授权动作被拒绝；生产配置不能启用任意主体；对象摘要不符/写入失败可重现。
- 交付：可执行启动/验证命令、选定版本、脱敏配置示例、身份适配限制、存储接口说明和迁移恢复证据；不得声称已有业务闭环。

## K03 资料、原件版本与解析定位

- 前置：K02 数据库/对象存储/身份基础；K01 资料和来源契约。可与 K05 并行，先用确定的知识引用边界对接。
- 允许写入：`OKB/internal/document/**`、`OKB/internal/repository/document/**`、`OKB/internal/service/document/**`、`OKB/internal/handler/document/**`、`OKB/test/integration/document/**`、`OKB/test/fixtures/document/**`。
- 产出：资料登记/导入、不可变原件版本、解析版本、PDF 文本层/HTML/人工文本解析、片段定位、重复和转载谱系、来源访问/更正状态及影响输入。
- 生产接口：确定版本的资料/解析/片段查询和原文读取、来源校验及更正事件；供 K04 提取、K05 复核、K06 发布和 K07 原文对照消费。
- 消费接口：K02 对象/事务/授权、K01 定位与来源字段；数据库结构需求交 K02，不能覆盖旧原件/解析或伪造网页页码。
- A 覆盖：主责 A01；支撑 A03—A05、A09、A12、A13。
- 独立验收：相同文件可解释去重，原件升级与解析器升级分别留旧版；旧片段可回查；转载不增加独立依据；扫描 PDF 返回明确未支持；来源更正可列出引用对象。
- 交付：合成文件摘要、版本及定位结果、来源更正/不可访问差异、异常路径证据和对外接口版本。

## K04 主题、提取、多来源候选组装与缺口补查

- 前置：K03 确定版本/片段接口及 K05 草稿/接纳接口稳定；消费 K02 worker/存储基础和 K01 提取契约。
- 允许写入：`OKB/internal/extractor/**`、`OKB/internal/worker/extraction/**`、`OKB/internal/repository/extraction/**`、`OKB/internal/repository/topic/**`、`OKB/internal/service/extraction/**`、`OKB/internal/service/topic/**`。
- 另可写入：`OKB/internal/handler/extraction/**`、`OKB/internal/handler/topic/**`、`OKB/test/integration/extraction/**`、`OKB/test/fixtures/extraction/**`。
- 产出：主题目标与范围、异步提取尝试、固定响应模型适配、原子陈述、字段出处、多来源候选、差异/冲突、缺口状态与补查记录；真实模型仅按获准配置接入。
- 生产接口：任务/尝试/候选/主题覆盖查询，向 K05 提交可追踪接纳输入；消费 K03 片段、K05 已有修订上下文、K02 对象/执行/授权接口。
- 保留原始输出与覆盖范围；领取/重试/取消有执行代次，旧响应不可覆盖当前结果；人工审核不占用 worker 租约；链式推导不能继承单边批准。
- A 覆盖：主责 A02—A04、A08；支撑 A05、A06、A11、A12。
- 独立验收：固定模型重放不完整单篇、多来源、版本冲突、转载、伪造出处、缺口补查；取消重试后的晚到响应被拒；无真实模型仍可查看已有内容。
- 交付：模型/提示词/Schema/上下文版本与尝试记录、离线重放方式、原始结果及校验输出、未覆盖范围，不声称主题已完整。

## K05 三类知识修订、来源与审核

- 前置：K02 独立持久化/身份授权；K01 三类知识和修订契约。可先用合成资料接口与 K03 并行，完成时须验证真实来源接口。
- 允许写入：`OKB/internal/repository/knowledge/**`、`OKB/internal/repository/review/**`、`OKB/internal/service/knowledge/**`、`OKB/internal/service/review/**`、`OKB/internal/handler/knowledge/**`、`OKB/internal/handler/review/**`、`OKB/test/integration/knowledge/**`。
- 产出：异常类型、告警映射、传播规则的稳定 ID/不可变修订；草稿并发保存；多来源断言；候选拆分/合并/接纳/驳回；审核冻结与退回沿革。
- 生产接口：确定修订读取、草稿与接纳、来源/依赖校验、提交/审核结果；供 K04 接纳与上下文、K06 发布、K07 编辑审核消费。
- 消费接口：K03 确定片段/来源（并行初期可按 K01 mock）、K02 事务/主体/授权；K04 候选以约定输入提交，不把模型输出变成批准状态。
- 审核绑定 revision_id 和摘要；规则引用确定异常修订；出处性质与知识提供者分开；来源更正保留历史并提供影响关系。
- A 覆盖：主责 A05—A07；支撑 A03、A09、A11—A13。
- 独立验收：三类型正反例；并发冲突；旧摘要批准失败；接纳重试幂等；拆分/合并可追溯；伪造来源/缺失依赖拒绝；退回产生新修订；无模型可人工编辑。
- 交付：真实持久化与身份下的请求/响应、修订/摘要/审核关联、K04/K06 可消费接口及未解决冲突。

## K06 发布、查询、索引、回退与撤回

- 前置：K05 确定修订/审核接口和 K03 来源接口稳定；K02 事务及对象存储可用。
- 允许写入：`OKB/internal/repository/release/**`、`OKB/internal/service/release/**`、`OKB/internal/service/query/**`、`OKB/internal/service/index/**`、`OKB/internal/handler/release/**`、`OKB/internal/handler/query/**`、`OKB/test/integration/release/**`。
- 产出：发布闭包校验、不可变 manifest/包、准备/校验/启用流程、正式与草稿查询、固定发布版本索引、来源更正影响、撤回与历史回退。
- 生产接口：发布/启用/回退/撤回操作、确定 release_id 查询、索引构建与状态、影响清单；供 K07、K08 消费。
- 消费接口：K05 批准修订和依赖、K03 来源与更正、K02 存储/事务/授权；不把索引作为另一权威写入口。
- 文件准备成功后再做事务与并发切换；记录失败与孤立对象清理方式，不假定 MySQL 和对象存储可原子提交；withdrawn 包拒绝重新启用，修正后构建新包。仍为 published 的历史包遇到来源问题时，重新评估后才可回退。
- A 覆盖：主责 A09、A10、A13；支撑 A05、A07、A12。
- 独立验收：未批准/缺依赖/重复 knowledge_id 被拒；文件故障不切启用指针；并发发布受控；查询固定版本；索引可从包重建；回退保留历史，撤回及来源更正列出影响。
- 交付：manifest/内容摘要、故障注入结果、启用指针变化、索引一致性及历史包检查证据。

## K07 前端知识工作台

- 前置：K01 后可按契约 mock 做页面；真实完成必须有 K02—K06 的 HTTP/授权/原文接口和明确配置。
- 仅此包允许写 Web：`UnifiedInfraWeb/src/components/knowledge/**`、`UnifiedInfraWeb/src/api/knowledge/**`、`UnifiedInfraWeb/src/composables/knowledge/**`、`UnifiedInfraWeb/src/services/knowledge/**`。
- 精确共享文件例外：`UnifiedInfraWeb/src/App.vue`、`UnifiedInfraWeb/src/main.ts`、`UnifiedInfraWeb/src/styles.css`、`UnifiedInfraWeb/src/main.test.ts`、`UnifiedInfraWeb/package.json`、`UnifiedInfraWeb/package-lock.json`；只作入口、注册、样式、必要依赖及对应验证。
- 加载隔离所需的例外：`UnifiedInfraWeb/src/composables/useManagement.ts` 及其对应测试；只为进入知识入口时停止/延迟同步来源加载与轮询，保留原有管理页面行为，不扩展拓扑逻辑。
- 产出：独立知识入口、资料及主题、原文/候选对照、三类知识修订、审核发布、历史查询；状态和知识请求独立，文件上传单独处理 Content-Type。
- 生产接口：用户操作与状态展示；消费 K01 契约和 K02—K06 真实 API；mock 适配显式标识，不能作为生产默认或声称数据已持久化。
- 入口需避免挂载知识页面即调用拓扑/CMDB；不得改 `TopologyDetails.vue`、现有拓扑组件/DTO/请求封装或借用其 sources 状态。若需导航恢复/分享复核，写明方案。
- A 覆盖：主责 A14 的知识入口；呈现 A01—A13 的状态与失败，重点 A05—A07、A11、A12；不能只凭截图证明后端约束。
- 独立验收：mock 阶段只验页面/契约；真实阶段验证上传、定位、并发冲突、审核与发布回退等操作；网络证据证明知识操作不请求拓扑，断开模型/CMDB 后仍可编辑；现有页面构建/测试及冒烟通过。
- 交付：mock/真实模式范围、网络记录、持久化和刷新恢复证据、实际构建/测试命令结果及允许文件 diff；不新增资产或图节点知识入口。

## K08 真实集成与验收

- 前置：K01—K07 均交付可消费接口和验证证据；统一版本在独立集成 checkout 中完成装配。
- 允许写入：`OKB/test/integration/e2e/**`、`OKB/test/fixtures/e2e/**`、`OKB/docs/development/handoffs/K08-*.md`；产品缺陷交责任包修复，不跨边界代改。
- 产出：真实 server/worker/MySQL/对象存储/浏览器闭环；固定响应模型离线基线，获准时另记真实模型结果；不连接实际拓扑。
- 生产接口：复现脚本、A01—A14 验收矩阵和问题清单；消费所有包的稳定契约、运行说明、配置及交付记录。
- A 覆盖：全量 A01—A14，特别复验跨包的接纳幂等、来源更正、审核冻结、文件故障、旧响应、撤回回退和无模型/无拓扑使用。
- 独立验收：从空独立测试库启动到发布查询及回退；保留每个 A 的输入、动作、结果、反例、请求/状态/摘要及对应提交；现有能力仅记录实际回归范围。
- 交付：实际环境与命令、通过/失败/未测清单、遗留风险、可恢复步骤；全部证据满足后才建议完成，不能用 mock、单测或文档齐全替代真实集成。

## 汇总门禁

| 验收 | 主责与协作 |
| --- | --- |
| A01 | K03，K02/K07；K08 复验 |
| A02—A04 | K04，K03/K05/K07；K08 复验 |
| A05—A07 | K05，K01/K03/K04/K07；K08 复验 |
| A08 | K04，K02/K07；K08 复验 |
| A09—A10 | K06，K02/K03/K05/K07；K08 复验 |
| A11—A12 | K02/K04/K05/K07 按层共同负责；K08 复验 |
| A13 | K06，K03/K05/K07；K08 复验 |
| A14 | K07，K01/K02 保障隔离；K08 复验 |

K01 为全部验收提供契约和合成样例，不承担所有真实行为的实现；K08 负责集成证据，不把领域缺陷转变为自己修改所有代码的权限。
