# 模型运行时契约

**状态：** 待评审  
**日期：** 2026-09-20  
**上游模型：** `docs/model/entities/`、`docs/model/relationships/`

## 1. 目标

本文定义权威模型进入 Go、MySQL 和 NebulaGraph 时必须统一遵守的身份、类型、关系、时间、诊断与发布规则。本文不重新定义业务字段；实体和关系语义仍以上游模型文档为准。

## 2. 实体目录

| Tag | 稳定身份字段 | 稳定身份来源 |
|---|---|---|
| `device` | `device_sn` | `device_view.device_sn` |
| `interface` | `port_uuid` | `port_view.port_uuid` |
| `gpu` | `parts_sn` | 实际 GPU SN `server_gpu.parts_sn` |
| `pod` | `uuid` | `pod.uuid` |
| `cabinet` | `uuid` | `cabinet.uuid` |
| `data_center` | `uuid` | `idc.uuid` |
| `rpp` | `inst_id` | `idc_RPP.inst_id` |
| `ups_group` | `inst_id` | `idc_ups_group.inst_id` |
| `ups` | `inst_id` | `idc_ups.inst_id` |
| `transformer` | `inst_id` | `idc_transformer.inst_id` |

每个节点都必须保存：

- `source_id STRING NOT NULL`
- 对应稳定身份字段：四类电力实体使用 `inst_id INT NOT NULL`；其他实体使用表中指定的 `STRING NOT NULL` 字段
- `created_at DATETIME NOT NULL`
- `synced_at DATETIME NOT NULL`

GPU 唯一身份为 `source_id + gpu + parts_sn`。`parts_sn` 仅 trim 首尾空白；缺失、空字符串或纯空白 SN 的卡记录直接忽略，不创建 GPU 或对应关系。`server_gpu.uuid` 不参与身份，也不保存到 GPU 节点；禁止 UUID/`inst_id` 回退、旧身份映射或双写。2026-11-06 更正：先按服务器 `condition.device_sn` 完整分页采集卡并识别有效/冲突 SN，再对有有效卡的服务器以 `condition.device_sn` 完整分页查询上联，不逐卡查询。返回的 `gpu_sn` 仅用于解析同一服务器所属的有效 `parts_sn`；缺失、未知 SN 或归属/属性冲突的记录跳过，服务器没有有效卡时不请求上联。`gpu_uplink.uuid` 仍是来源关系标识，不是卡身份。

实体文档列出的其他属性按以下类型映射：

| 领域类型 | NebulaGraph 类型 |
|---|---|
| 文本、枚举、原始 JSON 文本 | `STRING` |
| 布尔值 | `BOOL` |
| 有符号整数 | `INT`（64 位） |
| 浮点数 | `DOUBLE` |
| 时间点 | `DATETIME` |
| 列表或对象 | 规范化紧凑 JSON `STRING` |

NebulaGraph 的复合 list、set、map 不能作为节点或边属性，因此模型中的列表和对象必须按字段定义排序后编码为 UTF-8 紧凑 JSON；对象键按字典序排列，列表保持模型规定的业务顺序。缺失、空字符串和 NULL 的处理必须服从对应实体文档。未在实体文档中声明的字段不得写入图。

## 3. 规范身份与 VID

### 3.1 完整逻辑身份

完整逻辑身份由三部分组成：

1. `source_id`
2. 实体类型
3. 规范稳定身份字符串（`StableID`）

序列化格式固定为 UTF-8 JSON 数组，不转义斜杠、不添加额外空白：

```json
["cmdb-primary","device","SN123456"]
```

除四类电力实体外，禁止对稳定身份做大小写折叠、数字转换或路径拼接。来源文档明确要求的 trim、空值拒绝和字段归一化应在进入身份函数前完成。

`transformer`、`ups_group`、`ups`、`rpp` 的 `StableID` 使用来源正整数 `inst_id` 的规范十进制字符串，必须匹配 `[1-9][0-9]*` 且值不超过有符号 64 位整数上限；不得含正负号、前导零、空白、小数或指数。图中保存整数 `inst_id`，逻辑身份和哈希输入仍保存字符串，例如：

```json
["cmdb-primary","transformer","1588"]
```

这些 `inst_id` 只在 `source_id + 实体类型` 作用域内唯一；变化表示不同实体身份。缺失或非法值阻塞对应实体范围，不以 UUID、编码、标签或名称回退，不生成电力实体 UUID。其他实体的既定 UUID 或序列号身份不变。

指向四类电力实体的数字引用直接匹配同一来源、同一目标类型的 `inst_id`；确认目标存在且唯一后，以其规范 `StableID` 构造完整逻辑身份并生成端点 VID，不建立 `inst_id → UUID` 映射。数字引用本身不能绕过目标校验成为关系身份；其他实体仍按各自文档解析到既定稳定身份。

### 3.2 VID

VID 固定为以下字节串的 SHA-256 小写十六进制：

```text
["vertex", source_id, entity_type, stable_id]
```

NebulaGraph Space 使用 `FIXED_STRING(64)`。同一输入必须在所有进程、重试和同步批次中产生相同 VID。

Go 接口固定为：

```go
type EntityIdentity struct {
    SourceID   string
    EntityType EntityType
    StableID   string
}

func (i EntityIdentity) CanonicalJSON() ([]byte, error)
func (i EntityIdentity) VID() (string, error)
```

空 `source_id`、未知实体类型或空稳定身份必须返回错误；四类电力实体的 `StableID` 非规范正十进制字符串或超出有符号 64 位整数范围时也必须返回错误。

## 4. Edge Type 与合法端点

| Edge Type | `relation_kind` | 起点 | 终点 | 业务区分字段 |
|---|---|---|---|---|
| `spatial_relation` | `member_of` | `device` | `pod` | `plane` |
| `spatial_relation` | `located_in` | `device` | `cabinet` | 无 |
| `spatial_relation` | `located_in` | `cabinet` | `data_center` | 无 |
| `spatial_relation` | `tagged_with` | `cabinet` | `pod` | 无 |
| `composition_relation` | `owns_interface` | `device` | `interface` | 无 |
| `composition_relation` | `contains_gpu` | `device` | `gpu` | 无 |
| `network_relation` | `server_uplink` | `device` | `interface` | 无 |
| `network_relation` | `gpu_uplink` | `gpu` | `interface` | 来源关系 UUID |
| `network_relation` | `links_to` | `interface` | `interface` | 规范化端点对 |
| `power_relation` | `power_upstream` | `cabinet` | `rpp` | `power_path` |
| `power_relation` | `power_upstream` | `rpp` | `ups_group` | 无 |
| `power_relation` | `member_of` | `ups` | `ups_group` | 无 |
| `power_relation` | `power_upstream` | `ups_group` | `transformer` | 无 |
| `power_relation` | `has_standby` | `transformer` | `transformer` | 无 |

写入前必须同时校验 Edge Type、`relation_kind`、方向和两端 Tag。非法组合不得进入 nGQL。

`links_to` 是无向业务连接的单边存储表达：按两端完整逻辑身份的规范 JSON 字节序排序，小者为起点，大者为终点。

## 5. 边属性

所有边共同保存：

- `relation_id STRING NOT NULL`
- `relation_kind STRING NOT NULL`
- `source_id STRING NOT NULL`
- `created_at DATETIME NOT NULL`
- `synced_at DATETIME NOT NULL`

领域属性：

| Edge Type / 关系 | 属性 |
|---|---|
| `spatial_relation/member_of` | `plane INT NOT NULL` |
| `power_relation` 的 `cabinet → rpp` | `power_path STRING NOT NULL` |
| `network_relation/gpu_uplink` | `gpu_port`、`gpu_port_speed`、`gpu_ip`、`gpu_slot`、`server_port_speed`、`bond_name`、`tor_port`、`tor_port_speed`、`tor_role`、`source`，均为可空 `STRING` |
| `composition_relation` | 无额外属性 |

`gpu_uplink.tor_port` 是解析目标端口的必需来源字段；为空时不创建边。图属性保持可空，但由当前 builder 创建的边必须写入非空 `tor_port`。

## 6. `plane` 语义

规范值为：

- `1`：管理面
- `2`：计算面
- `3`：存储面
- `4`：带外管理面

`plane` 是 `device → pod/member_of` 的边属性和关系身份组成部分。未知非空整数原样保留并产生 warning；空值或无法解析为整数时不创建该关系并产生 resource-blocking 诊断。

## 7. `relation_id`

### 7.1 有来源关系 UUID

`gpu_uplink` 使用：

```text
SHA-256(["relation", source_id, "network_relation", "gpu_uplink", source_relation_uuid])
```

### 7.2 无来源关系 UUID

其他关系使用：

```text
SHA-256([
  "relation",
  source_id,
  edge_type,
  relation_kind,
  from_identity_json,
  to_identity_json,
  discriminator
])
```

`discriminator` 规则：

- `spatial_relation/member_of`：十进制 `plane`。
- `cabinet → rpp/power_upstream`：规范化 `power_path`。
- 其他关系：空字符串。

输出固定为小写 64 位十六进制。Go 接口固定为：

```go
type RelationIdentity struct {
    SourceID          string
    EdgeType          EdgeType
    Kind              RelationKind
    From              EntityIdentity
    To                EntityIdentity
    SourceRelationID  string
    Discriminator     string
}

func (i RelationIdentity) ID() (string, error)
func (i RelationIdentity) Rank() (int64, error)
```

## 8. Rank

- 同一端点、同一 Edge Type 只允许一条关系时使用 Rank `0`。
- 允许平行关系时，从 `relation_id` 的前 16 个十六进制字符解析为无符号 64 位整数，清除最高位后转换为正 `int64`；结果为 `0` 时改为 `1`。
- 同一端点、同一 Edge Type、同一 Rank 但 `relation_id` 不同视为哈希碰撞，必须终止受影响范围写入，禁止覆盖。
- 当前必须使用非零稳定 Rank 的关系是 `member_of` 和 `cabinet → rpp/power_upstream`；仓储实现可对所有边统一使用稳定 Rank，但查询不得依赖 Rank 表达业务语义。

## 9. 时间契约

- `created_at`：节点或关系首次成功写入图的 UTC 时间，后续 upsert 不得覆盖。
- `synced_at`：最近一次完整同步确认该对象存在的 UTC 时间。
- 系统时间转换为 UTC，并截断到微秒；同一轮同步的所有节点和关系使用完全相同的 `synced_at=T`。
- 来源时间解析后转换为 UTC，并截断到微秒。
- `0000-00-00`、Unix epoch 占位、明显超出业务范围或解析失败的来源时间写为 NULL，并产生 warning。

## 10. 同步、清理与发布

当前用户批准的同步流程：一次完整采集（含分页/目录查询），身份/全局冲突/端点校验→写图→独立查图库验收。一次完整采集可以包含多个分页、目录及引用解析请求；各阶段完成采集和校验后写图，写图后不再次读取 CMDB，不以第二遍来源差异产生 `source_changed`。独立查图库验收仍为成功条件；独立只读调试测试可比较两次来源调用，不属于生产同步流程。该流程及共享时间均不承诺 CMDB 原子快照或图写入原子可见性。

同步按以下依赖顺序采集和解析：

1. `data_center`、`pod`、`cabinet`
2. `transformer`、`ups_group`、`ups`、`rpp`
3. `device`
4. `interface`、`gpu`
5. `spatial_relation`、`composition_relation`
6. `network_relation`、`power_relation`

只有取得某资源类型的完整范围证明后，才允许清理该来源下本轮未见对象。同一来源的写入与清理必须串行：单进程由图仓储来源锁阻止写入和清理重叠，多实例由 MySQL 来源行锁保证同一来源只有一个活动执行者。清理顺序固定为：

1. 删除由该范围拥有、且本轮未见的边。
2. 删除仍无入边和出边、且由该范围拥有的节点。
3. 更新控制面发布状态。

分页失败、引用解析不完整或写入失败时，不得把受影响范围标记为完整，不得执行该范围清理。

## 11. 诊断分级

| 级别 | 含义 | 发布影响 |
|---|---|---|
| `fatal` | 图 Schema 不兼容、身份碰撞、Rank 碰撞、数据库或图写入失败 | 整个任务失败，不发布 |
| `resource-blocking` | 某资源范围采集不完整、必需稳定身份缺失、必需引用无法解析 | 该范围不发布、不清理；其他独立完整范围可继续 |
| `warning` | 未知但可保留的枚举、无效来源占位时间、可选字段异常 | 允许发布，必须统计并可查询 |

诊断至少包含：任务 ID、来源 ID、资源类型、来源对象标识、错误码、字段路径和脱敏后的摘要。

## 12. Schema 与查询约束

- 新 Schema 必须从本契约和 `docs/model/` 生成并评审，不从旧 nGQL 恢复。
- Schema 创建与业务进程启动分离；server 和 worker 不自动执行图 DDL。
- 所有 nGQL 外部值通过参数绑定传入。
- 索引只为已确认的读取路径创建；首批至少覆盖每个 Tag 的 `source_id + 稳定身份` 精确查找。
- 默认单元测试使用 fake client 校验生成的查询、参数、结果解码和错误映射。
- 真实图测试必须由显式环境变量启用，并使用专用可丢弃 Space。
