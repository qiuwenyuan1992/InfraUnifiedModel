# UnifiedInfraTopology AI 协作规则

本文件适用于 `UnifiedInfraTopology/` 及其全部子目录。

## 1. 指令优先级

出现冲突时，按以下顺序执行：

1. 用户当前明确指令。
2. `docs/model/entities/` 和 `docs/model/relationships/` 中的权威模型。
3. `docs/superpowers/specs/` 中的长期开发契约。
4. `docs/superpowers/plans/` 中已确认的阶段实施计划。
5. 当前代码和测试所表达的旧行为。

代码与权威模型冲突时，应修改代码，不得为了兼容旧代码而篡改模型。无法确定业务语义时，停止实现并询问用户。

## 2. 沟通要求

- 面向用户的说明、进度、问题和总结统一使用中文。
- 回复简洁直接，先说结论或正在执行的动作。
- 修改代码前说明目标；发现关键风险或改变方案时及时告知。
- 不用未经解释的英文术语堆砌文档；代码标识符、命令和产品名称可保留英文。

## 3. 产品硬约束

- 产品只服务单一拓扑域。
- 严禁添加 `scope_id`、`ScopeID`、`scopeID`、`TopologyScope`、`topology_scopes`、`/scopes` 或任何变相作用域字段。
- CMDB 上游原始字段 `in_monitor_scope_id` 只能保留在来源资料或来源 DTO 中，不得进入领域模型、API、数据库、图属性或身份。
- 必须保留 `source_id`，用于区分 CMDB 来源、组成稳定身份和执行来源级清理。
- 不设计租户隔离、多拓扑兼容层、旧 API 兼容层或旧数据库兼容路径。
- 当前权威模型固定为 10 个实体 Tag、4 个 Edge Type 和 14 种合法关系端点组合。
- 不创建 Address、NetworkNode、TerminationPoint、占位节点或按同步批次复制的资产节点。
- 数字 ID 只用于单次采集期间的引用解析，不得进入最终身份、VID、关系 ID 或持久化外键。

## 4. 存储职责

### MySQL

只保存控制面状态：

- 来源配置和启停状态。
- 同步任务、任务领取、重试和幂等信息。
- 来源游标、检查点和完整性证明。
- 诊断、统计和发布状态。
- 当前发布版本、图 Schema 版本和写入围栏。

不得保存图实体快照、关系快照、通用实体表、来源键表、身份绑定表或按批次复制的资产版本。

### NebulaGraph

只保存当前有效拓扑：

- 10 个实体 Tag。
- 4 个领域 Edge Type。
- 当前实体和关系属性。
- `created_at` 和 `synced_at`。

不得在图中保存来源历史版本。

## 5. 代码边界

- `api/v1`：HTTP DTO，不承载领域兼容逻辑。
- `internal/handler`：解析和返回 HTTP 请求，不直接访问数据库或来源系统。
- `internal/model`：领域类型、枚举、身份和值对象。
- `internal/adapter`：来源采集和来源 DTO，不直接写 MySQL 或 NebulaGraph。
- `internal/service`：采集编排、归一化、引用解析、校验、诊断和发布决策。
- `internal/repository`：控制面事务和图读写，不决定关系业务语义。
- `pkg/nebula`：NebulaGraph 客户端封装，不包含实体或关系规则。
- API handler 不得直接构造 nGQL。

## 6. 实现纪律

- 实现前先阅读相关模型文档、规格和当前阶段计划。
- 使用测试驱动开发：先写能够证明目标行为的失败测试，再写最小实现。
- 修复根因，不使用兼容字段、双写、静默降级或 SQL 回退掩盖模型冲突。
- 保持修改最小且聚焦，不顺带重构或增加未要求的功能。
- 外部值必须通过参数绑定传入 SQL 和 nGQL，不得拼接用户值、来源值或身份值。
- 集合输出必须确定性排序，不依赖 map、数据库或来源接口的自然顺序。
- VID、关系 ID 和 Rank 必须确定性生成；禁止使用随机 UUID 或写入顺序。
- 来源凭据只保存引用，不得写入日志、错误、任务详情或图属性。
- 未确认的来源语义不得自行推断；应保留原值并记录诊断，或阻止受影响资源发布。
- 系统生成时间统一使用 UTC、微秒精度；同一轮同步的节点和关系使用完全相同的 `synced_at=T`。
- 空字符串、缺失和 NULL 的含义服从对应实体文档，不得全局互换。

## 7. 数据库和迁移

- 当前没有必须兼容的生产存量数据，允许按已确认计划重置开发期 SQL 基线。
- SQL 基线必须保持 MySQL 与 SQLite 测试版本语义一致。
- server 和 worker 不得自动执行 NebulaGraph DDL。
- NebulaGraph Schema 必须作为独立文件评审和执行。
- 删除或重写迁移前，必须确认该操作属于用户已经批准的破坏性范围。

## 8. 测试与完成标准

按从小到大的顺序验证：

1. 运行直接受影响包的专项测试。
2. 运行：

   ```bash
   INVENTORY_GRAPH_TEST_CONFIG= TOPOLOGY_GRAPH_TEST_CONFIG= go test ./... -count=1 -timeout=90s
   ```

3. 运行：

   ```bash
   go vet ./...
   ```

4. 构建三个入口：

   ```bash
   go build ./cmd/server
   go build ./cmd/migration
   go build ./cmd/worker
   ```

5. 运行：

   ```bash
   git diff --check
   ```

需要真实 NebulaGraph 的测试必须显式启用；默认单元测试不得依赖外部服务。没有验证证据时，不得声称任务已经完成。

## 9. Git 与工作区安全

- 不覆盖、回滚或删除用户的无关修改。
- 发现意外工作区变更时停止操作并询问用户。
- 不使用 `git add .` 或 `git add -A`；必须按文件精确暂存。
- 未经用户明确要求不得提交、推送、变基或强制操作。
- 不跳过 Git hooks，不使用 `--no-verify`。
- 一个提交只包含一个可独立验证的任务。
- 删除文件、重置数据库或执行其他破坏性操作前，确认其处于用户批准范围内。

## 10. 当前实施入口

- 开发约定：`docs/superpowers/specs/2026-09-20-development-conventions.md`
- 运行时契约：`docs/superpowers/specs/2026-09-20-model-runtime-contract.md`
- 当前计划：`docs/superpowers/plans/2026-09-20-model-foundation.md`

实施时按计划复选框逐项推进。每个任务完成后立即记录验证结果，不得提前勾选后续任务。
