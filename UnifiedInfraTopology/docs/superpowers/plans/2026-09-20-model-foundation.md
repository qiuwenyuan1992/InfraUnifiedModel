# 模型基础设施实施计划

> **执行要求：** 实施本计划时，必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans`，逐项完成任务。所有步骤使用复选框（`- [ ]`）跟踪进度。

**目标：** 用单拓扑控制面、确定性拓扑身份、经过评审的 NebulaGraph Schema 和经过校验的图写入能力，替换已经废弃的资产快照基础设施。

**架构：** MySQL 只保存同步控制状态。Go 领域类型实现 10 类实体身份和 14 种合法关系端点组合。NebulaGraph 使用确定性的 64 字节 VID、稳定的关系 ID 和 Rank 保存当前拓扑；仓储代码必须先校验每次写入，再执行参数化 nGQL。

**技术栈：** Go 1.24、GORM、MySQL 8、SQLite 测试、NebulaGraph 3.x、`nebula-go/v3`、Testify。

**规格依据：** `docs/superpowers/specs/2026-09-20-model-runtime-contract.md`

## 全局约束

- 产品只服务一个拓扑域；不得添加租户或拓扑范围字段、参数、索引、身份前缀或兼容层。
- 每个实体和关系身份都必须保留 `source_id`。
- 权威业务模型是 `docs/model/**`。
- MySQL 只保存控制状态；不得保存图实体快照、关系候选、通用实体、来源键或身份绑定。
- NebulaGraph 只保存已经确认的 10 个标签（Tag）和 4 个边类型（Edge Type）。
- 不得添加 Address、NetworkNode、TerminationPoint、占位节点或按同步批次复制的资产节点。
- 外部值必须使用查询参数；禁止把身份值或来源值拼接进 nGQL。
- 每个任务都必须使用测试驱动开发，并形成可独立评审的提交。
- 未经用户明确要求，不得暂存或修改 `AGENTS.md`。

---

## 文件结构

### 需要替换的文件

- `internal/model/inventory.go`：用控制面记录替换废弃的范围、批次和快照结构。
- `internal/migration/mysql/0001_inventory.sql`：替换为全新的控制面基线。
- `internal/migration/sqlite/0001_inventory.sql`：提供与 MySQL 等价的 SQLite 测试基线。
- `internal/migration/mysql/0002_current_graph.sql`：删除；其中字段属于已经废弃的设计。
- `internal/migration/sqlite/0002_current_graph.sql`：删除。
- `internal/migration/schema_test.go`：围绕新的控制面表重写测试。
- `internal/repository/inventory_graph.go`：隔离调用方后，用拓扑写入能力替换旧的三类资源读取仓储。
- `internal/repository/inventory_graph_test.go`：替换废弃的查询预期。

### 需要创建的文件

- `internal/model/topology.go`：实体、边、关系和网络平面枚举。
- `internal/model/identity.go`：规范身份、VID、关系 ID 和 Rank 生成逻辑。
- `internal/model/identity_test.go`：确定性身份测试。
- `internal/model/relation.go`：关系端点矩阵和关系校验。
- `internal/model/relation_test.go`：全部合法及非法端点测试。
- `internal/migration/nebula/0001_topology.ngql`：经过评审的图 Schema 文件；进程启动时不自动执行。
- `internal/migration/nebula_schema_test.go`：检查标签、边类型、身份宽度、必需字段和禁止出现的旧类型。
- `internal/repository/topology_graph.go`：图写入仓储接口及实现。
- `internal/repository/topology_graph_test.go`：使用假客户端测试参数化更新和清理查询。

### 需要修改的文件

- `internal/migration/apply.go`：只加载迁移台账和新的控制面 SQL 迁移。
- `internal/repository/repository.go`：向新的控制面仓储提供控制数据库，不修改无关的用户仓储。
- `internal/service/sync_worker.go`：只调整编译所需的构造函数依赖；本计划不实现采集或发布。
- 只有构造函数签名发生变化时，才重新生成 `cmd/server/wire/`、`cmd/migration/wire/` 和 `cmd/worker/wire/` 下的 Wire 文件。

---

### 任务 1：重置 MySQL 和 SQLite 控制面基线

**涉及文件：**
- 修改：`internal/model/inventory.go`
- 修改：`internal/migration/apply.go:52-61`
- 替换：`internal/migration/mysql/0001_inventory.sql`
- 替换：`internal/migration/sqlite/0001_inventory.sql`
- 删除：`internal/migration/mysql/0002_current_graph.sql`
- 删除：`internal/migration/sqlite/0002_current_graph.sql`
- 替换：`internal/migration/schema_test.go`
- 修改：`internal/migration/apply_test.go`

**接口关系：**
- 产出：`model.Source`、`model.SyncRun`、`model.SyncCheckpoint`、`model.SyncDiagnostic`、`model.Publication`。
- 产出：两步迁移列表：`0000_ledger.sql`、`0001_inventory.sql`。
- 依赖：现有的迁移锁和校验和台账机制。

- [ ] **步骤 1：为新表集合编写失败测试**

断言迁移除了现有用户表和迁移台账外，只创建以下拓扑控制表：

```go
expected := []string{
    "sources",
    "sync_runs",
    "sync_checkpoints",
    "sync_diagnostics",
    "publications",
    "topology_schema_migrations",
}
```

断言以下废弃表不存在：

```go
for _, table := range []string{
    "generations",
    "entities",
    "source_keys",
    "identity_bindings",
    "device_versions",
    "interface_versions",
    "address_versions",
} {
    require.False(t, db.Migrator().HasTable(table), table)
}
```

增加以下约束测试：来源名称唯一、任务幂等、检查点唯一、诊断外键和发布版本单调递增。

- [ ] **步骤 2：运行迁移测试，确认旧基线下测试失败**

执行：

```bash
go test ./internal/migration -run 'TestSchema|TestApply' -count=1
```

预期：失败。因为旧表仍然存在，新控制表尚未创建。

- [ ] **步骤 3：定义控制面记录**

使用以下结构替换废弃模型：

```go
type Source struct {
    ID          string `gorm:"primaryKey"`
    Name        string
    AdapterKind string
    ConfigRef   string
    Enabled     bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type SyncRun struct {
    ID                string `gorm:"primaryKey"`
    SourceID          string
    Status            string
    Mode              string
    RequestHash       string
    IdempotencyKey    string
    RequestedBy       string
    CancelRequestedAt *time.Time
    CreatedAt         time.Time
    StartedAt         *time.Time
    FinishedAt        *time.Time
    ErrorCode         string
}

type SyncCheckpoint struct {
    SourceID    string `gorm:"primaryKey"`
    Resource    string `gorm:"primaryKey"`
    Cursor      string
    Complete    bool
    CompletedAt *time.Time
    UpdatedAt   time.Time
}

type SyncDiagnostic struct {
    ID           string `gorm:"primaryKey"`
    RunID        string
    SourceID     string
    Resource     string
    Severity     string
    Code         string
    ObjectRef    string
    FieldPath    string
    Detail       string
    CreatedAt    time.Time
}

type Publication struct {
    ID            string `gorm:"primaryKey"`
    RunID         string
    SourceID      string
    Version       int64
    SchemaVersion int
    Status        string
    PublishedAt   *time.Time
    CreatedAt     time.Time
}
```

保留显式 `TableName()` 方法，确保 SQL 表名稳定。

- [ ] **步骤 4：替换 SQL 基线**

创建 5 张控制表，并满足以下约束：

- ID 使用 ASCII 二进制排序规则。
- 同步任务使用 `UNIQUE(source_id, idempotency_key)`。
- 检查点使用 `PRIMARY KEY(source_id, resource)`。
- 任务、检查点和发布记录通过外键关联来源；诊断通过外键关联任务和来源。
- 发布记录使用 `UNIQUE(source_id, version)` 和 `UNIQUE(run_id)`。
- 不创建图资产表。

更新 `loadSteps`，只加载 `0000_ledger.sql` 和 `0001_inventory.sql`。

- [ ] **步骤 5：运行迁移测试**

执行：

```bash
go test ./internal/migration -count=1
```

预期：SQLite 测试、MySQL 迁移解析测试和迁移台账测试全部通过。

- [ ] **步骤 6：提交控制面基线**

```bash
git add internal/model/inventory.go internal/migration
git commit -m "refactor: 重置拓扑控制面数据库结构"
```

---

### 任务 2：实现确定性的实体与关系身份

**涉及文件：**
- 创建：`internal/model/topology.go`
- 创建：`internal/model/identity.go`
- 创建：`internal/model/identity_test.go`

**接口关系：**
- 产出：`EntityType`、`EdgeType`、`RelationKind`、`Plane`。
- 产出：`EntityIdentity.CanonicalJSON`、`EntityIdentity.VID`、`RelationIdentity.ID`、`RelationIdentity.Rank`。
- 依赖标准库：`encoding/json`、`crypto/sha256`、`encoding/hex`、`strconv`。

- [ ] **步骤 1：编写枚举和身份失败测试**

为全部 10 类实体编写表驱动测试：

```go
var entityTypes = []model.EntityType{
    model.EntityDevice,
    model.EntityInterface,
    model.EntityGPU,
    model.EntityPod,
    model.EntityCabinet,
    model.EntityDataCenter,
    model.EntityRPP,
    model.EntityUPSGroup,
    model.EntityUPS,
    model.EntityTransformer,
}
```

测试以下输入：

```go
identity := model.EntityIdentity{
    SourceID:   "cmdb-primary",
    EntityType: model.EntityDevice,
    StableID:   "SN123456",
}
```

必须精确序列化为：

```json
["cmdb-primary","device","SN123456"]
```

测试 VID 长度为 `64`、只包含小写十六进制字符、重复执行结果稳定、不同来源相互隔离、不同实体类型相互隔离，并拒绝空字段。

测试以下关系身份：来源关系 UUID、`plane`、`power_path`、规范化后的 `links_to` 端点以及 Rank 稳定性。

- [ ] **步骤 2：运行身份测试，确认测试失败**

```bash
go test ./internal/model -run 'TestEntityIdentity|TestRelationIdentity|TestRank' -count=1
```

预期：失败。因为新类型和方法尚不存在。

- [ ] **步骤 3：添加封闭枚举**

精确实现以下常量：

```go
const (
    EntityDevice EntityType = "device"
    EntityInterface EntityType = "interface"
    EntityGPU EntityType = "gpu"
    EntityPod EntityType = "pod"
    EntityCabinet EntityType = "cabinet"
    EntityDataCenter EntityType = "data_center"
    EntityRPP EntityType = "rpp"
    EntityUPSGroup EntityType = "ups_group"
    EntityUPS EntityType = "ups"
    EntityTransformer EntityType = "transformer"
)
```

添加 4 个边类型、全部关系类型，以及整数网络平面 `1`、`2`、`3`、`4`。校验方法必须拒绝未知实体类型和未知边类型。解析 `plane` 时保留未知的非空整数，同时返回该值是否属于规范值域。

- [ ] **步骤 4：实现规范 JSON 和哈希**

规范数组使用 `json.Marshal([]string{...})`；ID 使用 `sha256.Sum256` 和 `hex.EncodeToString`。禁止使用冒号拼接身份。

生成 Rank 时，解析关系 ID 前 16 个十六进制字符，清除符号位，结果为零时改为一；关系 ID 格式错误时返回错误。

- [ ] **步骤 5：运行身份测试**

```bash
go test ./internal/model -run 'TestEntityIdentity|TestRelationIdentity|TestRank' -count=1
```

预期：全部通过。

- [ ] **步骤 6：提交身份基础能力**

```bash
git add internal/model/topology.go internal/model/identity.go internal/model/identity_test.go
git commit -m "feat: 添加确定性拓扑身份"
```

---

### 任务 3：编码并测试关系端点矩阵

**涉及文件：**
- 创建：`internal/model/relation.go`
- 创建：`internal/model/relation_test.go`

**接口关系：**
- 依赖：任务 2 中的枚举和身份类型。
- 产出：`Relation`、`RelationSpec`、`ValidateRelation`、`NormalizeRelation`。

```go
type Relation struct {
    Identity   RelationIdentity
    Properties map[string]any
    CreatedAt  time.Time
    SyncedAt   time.Time
}

type RelationSpec struct {
    EdgeType EdgeType
    Kind     RelationKind
    From     EntityType
    To       EntityType
}

func NormalizeRelation(Relation) (Relation, error)
func ValidateRelation(Relation) error
```

- [ ] **步骤 1：为 14 种合法组合分别编写成功测试**

测试表必须覆盖运行时契约中的每一行。增加以下非法测试：方向反转、边类型错误、未知关系类型、缺少 `plane`、缺少 `power_path`、GPU 来源关系 UUID 为空、备用变压器指向自身。

- [ ] **步骤 2：编写关系规范化测试**

断言 `links_to` 会把端点调整为规范顺序、保留属性，并且从任意来源方向输入都生成相同的关系 ID。

断言 `member_of` 的关系身份包含十进制 `plane`；机柜电力关系在生成 ID 前按照模型文档规范化 `power_path`。

- [ ] **步骤 3：运行测试，确认测试失败**

```bash
go test ./internal/model -run 'TestRelationMatrix|TestNormalizeRelation|TestValidateRelation' -count=1
```

预期：失败。因为关系校验尚未实现。

- [ ] **步骤 4：实现封闭的端点矩阵**

使用包内私有 map，以 `{EdgeType, RelationKind, From, To}` 为键。不得根据名称或属性推断关系是否合法。校验必须检查公共时间、关系与两端的来源一致性、必需的业务区分字段及关系专属属性。

- [ ] **步骤 5：运行模型测试**

```bash
go test ./internal/model -count=1
```

预期：全部通过。

- [ ] **步骤 6：提交关系契约**

```bash
git add internal/model/relation.go internal/model/relation_test.go
git commit -m "feat: 校验拓扑关系"
```

---

### 任务 4：创建 NebulaGraph v1 Schema 文件

**涉及文件：**
- 创建：`internal/migration/nebula/0001_topology.ngql`
- 创建：`internal/migration/nebula_schema_test.go`

**接口关系：**
- 依赖：任务 2 和任务 3 定义的实体及关系名称。
- 产出：供专用可丢弃 Space 或生产 Space 人工评审和执行的 Schema。
- 不修改：`migration.Apply`；SQL 迁移和图 Schema 执行必须分离。

- [ ] **步骤 1：编写失败的 Schema 契约测试**

在测试中嵌入或读取 nGQL 文件，并断言：

- `CREATE SPACE` 使用 `vid_type = FIXED_STRING(64)`。
- 精确存在 10 条 `CREATE TAG` 语句。
- 精确存在 4 条 `CREATE EDGE` 语句。
- 每个标签都有 `source_id`、对应稳定身份字段、`created_at` 和 `synced_at`。
- 每个边类型都有 `relation_id`、`relation_kind`、`source_id`、`created_at` 和 `synced_at`。
- `spatial_relation` 支持可空或具有安全默认值的 `plane INT`。
- 除非当前实体文档明确声明，否则不得出现已废弃的 `address`、批次、生命周期和解析字段。

- [ ] **步骤 2：运行测试，确认测试失败**

```bash
go test ./internal/migration -run TestNebulaSchemaContract -count=1
```

预期：失败。因为 Schema 文件尚不存在。

- [ ] **步骤 3：编写 nGQL 文件**

按以下评审阶段组织：

1. 使用 `CREATE SPACE IF NOT EXISTS unified_infra_topology` 创建 Space，并写入明确的开发环境分区数和副本数，不保留占位符。
2. 执行 `USE unified_infra_topology`。
3. 根据 `docs/model/entities/` 中的精确属性创建 10 个标签。
4. 根据运行时契约中的公共属性和领域属性创建 4 个边类型。
5. 只创建身份查询索引：`source_id` 加每个标签的稳定身份字段。

不得包含数据修改语句或自动重建索引命令。

- [ ] **步骤 4：运行 Schema 测试**

```bash
go test ./internal/migration -run TestNebulaSchemaContract -count=1
```

预期：全部通过。

- [ ] **步骤 5：提交图 Schema**

```bash
git add internal/migration/nebula/0001_topology.ngql internal/migration/nebula_schema_test.go
git commit -m "feat: 定义拓扑图数据库结构"
```

---

### 任务 5：实现经过校验的图更新能力

**涉及文件：**
- 替换：`internal/repository/inventory_graph.go`
- 替换：`internal/repository/inventory_graph_test.go`
- 创建：`internal/repository/topology_graph.go`
- 创建：`internal/repository/topology_graph_test.go`
- 修改：`internal/repository/repository.go`

**接口关系：**
- 依赖：`model.EntityIdentity`、`model.Relation`、关系校验函数和 `pkg/nebula.Client`。
- 产出：

```go
type TopologyVertex struct {
    Identity   model.EntityIdentity
    Properties map[string]any
    CreatedAt  time.Time
    SyncedAt   time.Time
}

type TopologyGraphRepository interface {
    UpsertVertex(context.Context, TopologyVertex) error
    UpsertRelation(context.Context, model.Relation) error
    DeleteRelationsNotSeen(context.Context, string, model.EdgeType, time.Time) error
    DeleteOrphanVerticesNotSeen(context.Context, string, model.EntityType, time.Time) error
}
```

- [ ] **步骤 1：使用假客户端编写节点更新失败测试**

断言：

- VID 通过参数传入。
- 所有属性值都通过参数传入。
- 标签名称只能来自封闭枚举。
- `created_at` 只在首次写入时设置。
- 每次成功观察到节点时都更新 `synced_at`。
- 执行客户端调用前拒绝未知属性。

- [ ] **步骤 2：使用假客户端编写关系更新失败测试**

断言执行查询前完成端点校验；`relation_id` 和 Rank 必须确定性生成；方向非法或缺少必需属性时，客户端调用次数必须为零。

- [ ] **步骤 3：编写清理失败测试**

断言清理查询必须同时受 `source_id`、实体或边类型以及 `synced_at < run_started_at` 限制。必须先清理边，再清理孤立节点。采集不完整时不得调用清理方法；是否允许发布属于服务层职责，本测试只通过显式仓储调用验证清理查询。

- [ ] **步骤 4：运行仓储测试，确认测试失败**

```bash
go test ./internal/repository -run 'TestTopologyGraph' -count=1
```

预期：失败。因为新接口和实现尚不存在。

- [ ] **步骤 5：实现最小化的参数化 nGQL**

标签和边标识符必须来自封闭枚举。属性子句只能根据每种类型的允许字段列表构建。身份、Rank、时间和属性必须通过 `ExecuteParameter` 参数传入。

将传输失败、空结果、查询被拒绝和结果格式错误映射为明确的仓储错误；错误文本不得包含查询参数。

- [ ] **步骤 6：删除废弃的三类资源图行为**

删除关于 `devices`、`interfaces`、`addresses`、旧 VID 前缀、图批次读取和生命周期过滤的假设。只修改直接导致编译失败的调用方；本计划不添加替代读取 API。

- [ ] **步骤 7：运行仓储和依赖服务测试**

```bash
go test ./internal/repository ./internal/service -count=1
```

预期：全部通过。旧读取行为测试必须删除，或改写为明确断言：在后续读取 API 计划完成前，该能力不可用。

- [ ] **步骤 8：提交图写入能力**

```bash
git add internal/repository internal/service/sync_worker.go
git commit -m "feat: 添加拓扑图写入能力"
```

---

### 任务 6：最终集成验证

**涉及文件：**
- 只修改解决任务 1 至任务 5 所引入编译错误所必需的文件。
- 只有构造函数签名发生变化时，才重新生成 Wire 文件。

**接口关系：**
- 验证全部交付物；不增加新的产品行为。

- [ ] **步骤 1：格式化修改过的 Go 文件**

```bash
gofmt -w internal/model internal/migration internal/repository internal/service
```

暂存前必须核对准确的文件变更列表。

- [ ] **步骤 2：运行重点包测试**

```bash
go test ./internal/model ./internal/migration ./internal/repository ./internal/service -count=1
```

预期：全部通过。

- [ ] **步骤 3：运行完整测试套件**

```bash
INVENTORY_GRAPH_TEST_CONFIG= go test ./... -count=1 -timeout=90s
```

预期：全部通过，并且不依赖真实 NebulaGraph 服务。

- [ ] **步骤 4：运行静态检查**

```bash
go vet ./...
```

预期：没有问题。

- [ ] **步骤 5：构建全部进程入口**

```bash
go build ./cmd/server
go build ./cmd/migration
go build ./cmd/worker
```

预期：3 条命令全部成功退出。

- [ ] **步骤 6：检查模型与差异约束**

```bash
! grep -R "device_versions\|address_versions" internal/model internal/migration internal/repository
git diff --check
git status --short
```

预期：不存在废弃拓扑结构，不存在空白字符错误，不存在无关的暂存文件。

- [ ] **步骤 7：申请代码评审**

对本计划的实现范围使用 `superpowers:requesting-code-review`。解决评审发现的正确性问题，重新执行步骤 2 至步骤 6，并记录最终验证结果。

- [ ] **步骤 8：提交仅用于集成的调整**

只有格式化或依赖注入生成过程修改了以下已评审文件时，才创建此提交：

```bash
git add cmd/server/wire/wire_gen.go cmd/migration/wire/wire_gen.go cmd/worker/wire/wire_gen.go
git commit -m "chore: 集成拓扑模型基础设施"
```

---

## 不在本计划范围内

以下内容必须分别编写规格和实施计划，并单独评审：

- CMDB HTTP 采集和分页。
- 来源 DTO 映射和引用解析。
- Worker 轮询、任务领取、重试和发布编排。
- 读取 API 和图查询设计。
- 身份认证或用户模块修改。
- 生产环境 NebulaGraph 上线、备份和数据迁移。

## 完成证据

全部任务完成后，在本文件末尾追加简短执行记录，包含：

- 任务 1 至任务 6 的提交哈希；
- 实际执行的验证命令及退出状态；
- 已获批准的运行时契约偏差；
- 确认没有包含任何无关文件。
