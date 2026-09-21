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
| `gpu` | `uuid` | `server_gpu.uuid` |
| `pod` | `uuid` | `pod.uuid` |
| `cabinet` | `uuid` | `cabinet.uuid` |
| `data_center` | `uuid` | `idc.uuid` |
| `rpp` | `uuid` | `idc_RPP.uuid` |
| `ups_group` | `uuid` | `idc_ups_group.uuid` |
| `ups` | `uuid` | `idc_ups.uuid` |
| `transformer` | `uuid` | `idc_transformer.uuid` |

每个节点都必须保存：

- `source_id STRING NOT NULL`
- 对应稳定身份字段 `STRING NOT NULL`
- `created_at DATETIME NOT NULL`
- `synced_at DATETIME NOT NULL`

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
3. 稳定身份原值

序列化格式固定为 UTF-8 JSON 数组，不转义斜杠、不添加额外空白：

```json
["cmdb-primary","device","SN123456"]
```

禁止对稳定身份做大小写折叠、数字转换或路径拼接。来源文档明确要求的 trim、空值拒绝和字段归一化应在进入身份函数前完成。

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

空 `source_id`、未知实体类型或空稳定身份必须返回错误。

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
- 系统时间截断到秒。
- 来源时间解析后转换为 UTC，并截断到秒。
- `0000-00-00`、Unix epoch 占位、明显超出业务范围或解析失败的来源时间写为 NULL，并产生 warning。

## 10. 同步、清理与发布

同步按以下依赖顺序采集和解析：

1. `data_center`、`pod`、`cabinet`
2. `transformer`、`ups_group`、`ups`、`rpp`
3. `device`
4. `interface`、`gpu`
5. `spatial_relation`、`composition_relation`
6. `network_relation`、`power_relation`

只有取得某资源类型的完整范围证明后，才允许清理该来源下本轮未见对象。清理顺序固定为：

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
