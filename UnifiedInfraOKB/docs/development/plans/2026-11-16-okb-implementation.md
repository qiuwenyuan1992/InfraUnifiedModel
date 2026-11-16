# UnifiedInfraOKB Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 让运维专家在独立知识工作台完成多来源资料整理、AI 候选提取、知识修订审核、固定版本发布与追溯查询。

**Architecture:** 同仓库独立 Go module 和 server/worker，独立 MySQL 知识数据库，文件/对象存储保存不可变大对象。复用 UnifiedInfraWeb 的 Vue 控制台，知识流程不依赖实际拓扑。

**Tech Stack:** Go、MySQL、Vue 3、TypeScript、Element Plus；具体服务依赖、解析器和模型 SDK 由责任任务选择并固定版本。当前尚无 OKB 运行时初始化。

**Spec:** [需求基线](../../design/requirements.md)、[存储](../../design/storage-model.md)、[流程](../../design/workflows-and-ui.md)、[接口边界](../../design/api-boundaries.md)。

## Global Constraints

- 当前建设独立知识管理流程，不使用 TopologyDetails.vue，不接入实际拓扑或具体资产。
- 在同一 Git 仓库新增独立 UnifiedInfraOKB 后端模块，前端复用 UnifiedInfraWeb。
- 知识正文和版本以独立 MySQL 知识库为权威；原件及大文件使用文件或对象存储；发布导出不可变知识包。
- 三种正式 kind 固定为 anomaly_type、alert_mapping、causal_rule。原子陈述是候选/组装材料，不是第四类正式知识。
- AI 输出是候选；专家通过网页处理，接纳、复核、发布分别记录。
- 草稿并发检查，审核冻结确定内容和摘要；正式引用使用固定 revision_id，发布校验依赖闭包。
- 文件准备及校验先完成，再切换数据库启用指针；不假设文件与 MySQL 原子提交。
- 第一期开启有文本层 PDF、HTML、人工文本；扫描件/OCR 未实现时明确未支持，不返回空成功。
- OpenSPG/KAG、向量库、实际 CMDB/拓扑均不是本期必装依赖。
- 仅合成或获准数据；生产连接和内部资料外传不在本轮授权内。用户已追加授权提交推送这套骨架和文档；未来功能任务的 Git 动作另按当时授权执行。

## Review Focus

- 同字节重复、供应方版本冲突、解析器升级后的旧片段定位：K03 对应 A01/A04。
- 不完整单篇、版本差异、转载和 Top-K 未命中：K04 对应 A02—A04。
- 候选重复接纳、并发编辑和审核旧摘要：K05 对应 A05—A07。
- 取消/重试/租约过期后的模型晚到结果：K04 对应 A08。
- 文件已写而 SQL 失败、依赖双版本、撤回后的回退：K06 对应 A09/A10/A13。

## 计划使用方式

状态：planned，未执行。以下任务的产物和测试是未来要求，不是本轮已有文件或通过结果。

本计划分配接口和领域所有权。K01 先冻结机器契约及字段字典，再由每个执行者在任务简报中填入确定的函数签名、库版本、测试运行命令与环境；此时不伪造尚不存在的 Go API、DDL 或启动命令。

接手环境有上述技能时按其流程实现；其他 AI 无需安装特定插件，按工作包、失败测试、独立审阅和真实交付证据推进。用户当前任务只是目录和交接文档，不要求在本轮执行 K01—K08。

每包都按“明确契约→写能证明目标的失败测试→运行确认失败→最小实现→专项及集成验证→交付”执行。只有当前任务另有授权时才精确提交或推送，不从计划获得隐含权限。

## Task K01 契约、Schema 与合成验收

**Files:** 创建 api/v1/openapi.yaml、api/v1/schemas/ 下的 document、topic、extraction、knowledge、release Schema，api/v1/field-dictionary.md，以及 test/fixtures/synthetic/ 下的 A01—A14 输入和期望；internal/model 的公共类型由本包维护。全部相对 UnifiedInfraOKB。

**Interfaces:** 消费四份设计文档；产出单一 contract_version、DTO/领域类型、适配器职责契约、状态/错误/摘要/并发字典和 manifest。K02—K07 只能消费此版本或申请变更。

- [ ] 固定 ID、UTC 时间、大小/格式预算、状态、上传/下载、身份动作、分页、幂等及 row_version 契约，记录默认值和可配置边界。
- [ ] 设计合成反例：不存在的片段、断言与出处不符、缺异常修订、同包同条目双版本、已撤回版本、过期摘要和非法状态。
- [ ] 先用反例证明契约校验会拒绝，再补齐 Schema/条件校验；选择并记录实际校验工具，不用“文件是合法 JSON”替代语义验证。
- [ ] 给 K02—K07 明确消费样例，校对 candidate_kind 与三类 knowledge kind、审核与发布状态的区分。
- [ ] 交付确定的版本、验收矩阵、消费者清单和未决部署配置；只声明契约完成，不声明业务服务完成。

**Gate:** A01—A14 均有字段/状态与验证责任，Schema 正反例有实际执行证据。

## Task K02 Go、独立存储和身份基础

**Files:** go.mod/go.sum、cmd/server、cmd/worker、config，internal/authorization、storage、repository/database、service/runtime、handler/runtime、handler/router.go、worker/runtime、migrations/mysql、test/integration/foundation。

**Interfaces:** 消费 K01 的主体/授权、DocumentStore、配置、事务、错误和 worker 装配契约；向后续包提供独立数据库、迁移台账、不可变文件及授权基础。公共依赖、迁移序号和路由装配由本包唯一维护。

- [ ] 用 test/fake identity 和独立测试存储编写用例：身份缺失、权限缺失、摘要错误、文件写入失败、配置误指拓扑库均不能成功。
- [ ] 运行并确认失败，再初始化独立 Go module、必要依赖、分层装配及接口实现。
- [ ] 建立 server/worker 分进程和受控停机；生产配置缺少获准身份/存储时明确失败，不能采用测试主体兜底。
- [ ] 在实际独立 MySQL 中验证迁移账本、事务和对象状态，验证文件后端暂存/不可变/校验行为；共享事务键不能串到另一连接。
- [ ] 记录真实版本、启动/测试/构建命令及结果，提供无秘密配置示例和恢复说明。

**Gate:** A01/A07—A09/A11/A12/A14 的基础保障通过；没有业务实现时不标记整个闭环可用。

## Task K03 资料原件、解析版本和出处

**Files:** internal/document、repository/document、service/document、handler/document、test/integration/document、test/fixtures/document；迁移需求交 K02。

**Interfaces:** 消费 K01/K02；产出固定 document_version/parse_version/fragment 查询与读取、解析覆盖清单、来源谱系和更正影响输入。供 K04/K05/K06/K07 消费。

- [ ] 写失败测试：重复原件、新原件、同字节版本声明冲突、解析器升级、旧片段定位、转载谱系、扫描件未支持和 HTML 无页码。
- [ ] 先证实失败，再实现登记/导入、版本、解析、片段、覆盖及来源状态；不得覆盖旧原件或旧定位。
- [ ] 用合成 PDF/HTML/文本验证可回查，片段 ID 与父版本必须一致，定位/摘录摘要错误应被发现。
- [ ] 验证原文不可访问与内容更正不同，已保存可读快照不因原站临时断线自动变成证伪。
- [ ] 提供可供并行包消费的契约和实际结果，记录解析格式和未实现能力。

**Gate:** 主责 A01，支撑 A03—A05/A09/A12/A13。

## Task K05 三类知识、接纳及审核

**Files:** internal/repository、service、handler 各自 knowledge/review 子目录，test/integration/knowledge。知识公共类型归 K01，迁移归 K02。

**Interfaces:** 消费 K01/K02 和 K03 的固定来源接口；初期可按稳定契约用明确 mock 并行。产出草稿/冻结修订、来源/依赖校验、候选接纳协议、审核读取与决定，供 K04/K06/K07 消费。

- [ ] 写失败测试：两个会话旧 row_version 保存、审核错误 hash、父修订跨 knowledge_id、伪造 fragment、重复接纳、assertion 直接当正式知识、缺失或错误 kind 的依赖。
- [ ] 运行确认失败，再实现三类正文、多候选条件、出处、依赖和允许用途；接纳原子陈述只成为材料。
- [ ] 用事务实现拆分/合并/接纳及幂等；提交审核冻结正文/出处/依赖/缺口摘要，退回修改生成新修订。
- [ ] 用真实 MySQL 和真实主体适配验并发及授权，复验固定来源接口，不以 mock 替代完成。
- [ ] 交付 revision/hash/依赖与审核记录示例，供 K04/K06 对接；每条重要断言可回到原文或明确经验依据。

**Gate:** 主责 A05—A07，支撑 A03/A09/A11—A13。K03 与 K05 在 K02 后可并行，K04 需二者接口可用。

## Task K04 主题、多来源提取与候选组装

**Files:** internal/extractor、worker/extraction、repository/extraction、repository/topic、service/extraction、service/topic、handler/extraction、handler/topic、test/integration/extraction、test/fixtures/extraction。

**Interfaces:** 消费 K03 固定解析/片段、K05 草稿与已有知识上下文、K02 worker/存储/授权；产出主题问题、覆盖/缺口、attempt、两类候选、组装/补查记录。

- [ ] 用固定响应编写失败测试：单篇缺条件、多份互补、版本冲突、转载、漏处理区间、伪造出处及 A→B/B→C 未经核实组合。
- [ ] 加入执行测试：attempt 1 超时后 attempt 2 成功，旧响应后到；取消后旧租约提交；无模型配置；反例必须保持当前结果和缺口正确。
- [ ] 运行失败测试后实现冻结输入 manifest、覆盖提取、候选校验、组装及定向补查；人工待审不占 worker 租约。
- [ ] 先验证固定响应模式，再仅在获准配置下接真实模型。记录两种模式，不能把固定响应称为真实模型效果。
- [ ] 交付输入/提示词/模型/Schema/上下文版本、完成与失败范围、工作摘要和候选去向，供 K05/K07 接入。

**Gate:** 主责 A02—A04/A08，支撑 A05/A06/A11/A12；模型不补出没有出处的完整知识。

## Task K06 固定发布、查询及撤回回退

**Files:** internal/repository/release、service/release、service/query、service/index、handler/release、handler/query、test/integration/release。

**Interfaces:** 消费 K05 批准修订/固定依赖、K03 来源、K02 事务/文件/权限；产出发布包、manifest、启用历史、版本查询、影响清单与可重建索引。

- [ ] 写失败测试：未批准修订、缺依赖、同 knowledge_id 双修订、文件写失败、文件成功而 SQL 失败、并发切换、撤回包再次启用。
- [ ] 运行确认失败，再实现闭包和快照、building 构建、对象校验及 published 固定包；按通道并发版本切启用指针。
- [ ] 验证正式查询锁定 release_id，草稿有明确标签；索引混版被阻断，可从固定包重建。
- [ ] 验证来源更正/撤回影响和历史回退；发布/启用/回退/撤回各留审计，不覆盖历史正文。
- [ ] 在真实 MySQL 与文件后端注入故障，交付 manifest、指针变化、幂等恢复和未回收对象处理证据。

**Gate:** 主责 A09/A10/A13，支撑 A05/A07/A12；K06 可在 K05 与来源接口稳定后推进。

## Task K07 专家知识前端

**Files:** 按 [工作包](../work-packages.md) 的精确 Web 清单：新增 knowledge 组件/client/composables/services，必要的 App/main/styles/入口测试，以及 useManagement 的加载隔离和相应测试；不修改 TopologyDetails 或拓扑 DTO。

**Interfaces:** 消费 K01 契约与 K02—K06 的实际 API；可先显式 mock 做 UI，但真实完成需整个后端。

- [ ] 写页面与请求断言：进入知识工作区不触发拓扑查询，CMDB 不可用不阻断编辑；上传不错误使用 JSON 头，身份缺失/权限失败可见。
- [ ] 再实现资料、主题原文对照、三类编辑、审核发布和历史查询；复用 Vue/Element Plus，知识状态与同步状态独立。
- [ ] 写并发保存、未保存离开、刷新恢复、晚到响应、无模型配置和字段级出处的操作测试；mock 界面明确标记。
- [ ] 接真实服务验证资料导入到发布查询/回退，检查请求和持久化，不以截图代替后端约束。
- [ ] 执行 UnifiedInfraWeb 实际 package.json 的 build/test 并记录输出，复验原有菜单与本期允许路径；新增锁文件依赖只由本包维护。

**Gate:** 主责 A14，呈现 A01—A13。无真实后端时只能交付 UI 部分完成。

## Task K08 集成验收与最终交付

**Files:** test/integration/e2e、test/fixtures/e2e 和本任务交付记录；不取得其他业务路径的默认写权限。

**Interfaces:** 消费 K01—K07 的固定契约、实现提交和运行说明，产出可复现的端到端证据与剩余问题。

- [ ] 在独立集成 checkout 核对版本和允许配置，从空测试库、合成资料和固定响应启动真实 server/worker/存储/Web。
- [ ] 逐个执行 A01—A14，给每项记录输入、动作、结果、失败/未知分支、对象 ID、摘要及接口证据。
- [ ] 对跨包失败定位责任包修复并复验，不用绕过校验、直接改库或静默降级获得通过。
- [ ] 将真实模型验证单独记录；没有授权或配置则标未测，不影响如实评价固定响应流程是否通过。
- [ ] 按交付模板记录迁移/恢复、依赖、真实命令与结果及仍缺条件；只有证据完整才建议集成完成。

## 本计划的自检记录

- 需求覆盖：A01—A14 均有责任任务，范围与数据/流程设计一致。
- 任务边界：K01 公共契约，K02 依赖/迁移/装配，K03 与 K05 可并行，K04 依赖其稳定接口，K07 为 Web 唯一写入者。
- 类型一致：三类正式知识与 assertion/knowledge_draft 两类候选分开；job、revision、release、active 分开。
- 当前执行证据：本轮没有执行任何产品任务，不声称上述测试已通过。
- 后续详细计划：每包领取后将 K01 确定契约及责任包实际版本填入任务简报；存在共享语义冲突时先更新契约和影响任务。

[返回开发接手入口](../README.md)
