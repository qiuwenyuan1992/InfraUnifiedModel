# POD 实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于表达设备在管理面、计算面、存储面和带外管理面中的逻辑归属。

## 1. 设计目标

`pod` 表示 CMDB `pod` 对象。所有平面统一使用一种 POD 节点，不为管理面、计算面或其他平面拆分不同实体类型。

本实体遵守以下边界：

1. POD 节点以 `pod.uuid` 作为最终身份。
2. 按已确认要求保留 `inst_id`、`phy_building_id` 和 `idc_logic_id` 等数字 ID，但它们不替代 UUID，也不直接生成楼宇或逻辑 IDC 节点。
3. 管理面设备优先使用稳定 `pod_uuid` 建边；计算面设备使用 `compute_plane[].pod_id` 按 `pod.inst_id` 查询 POD UUID。
4. `compute_plane` 是设备侧关系来源，不建立独立节点。
5. 当前只建立 `device → pod` 的 `member_of` 关系；不建立 `pod → data_center`、`pod → building` 或 `pod → logic_idc`。
6. NebulaGraph 保存 POD 节点和已解析成功的关系；MySQL 不保存 POD 快照或未解析关系候选。

## 2. POD 查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `pod`：

```json
{
  "fields": [],
  "page": {
    "start": 0,
    "limit": 100,
    "sort": "inst_id"
  },
  "condition": {}
}
```

只有全部分页成功后，该来源和范围的 POD 节点集合才可视为完整。

### 2.2 按数字引用查询

设备计算面引用只提供 `pod_id` 时，按 `pod.inst_id` 查询：

```json
{
  "condition": {
    "inst_id": 2289
  }
}
```

查询成功后使用返回的 `pod.uuid` 作为关系终点。`pod_id` 和 `inst_id` 仅用于来源解析，不能直接作为图 VID。

### 2.3 按平面查询

接口支持按 `plane` 过滤：

```json
{
  "condition": {
    "plane": 2
  }
}
```

平面过滤查询用于局部核对或业务查询，不可替代完整全量采集，也不能据此清理其他平面的 POD。

## 3. POD 图节点

### 3.1 身份

POD 最终身份使用：

```text
pod.uuid
```

逻辑 POD 身份：

```text
source_id:pod:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `basic_code`
- `name`
- `full_name`
- `phy_building_id`
- `idc_logic_id`

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `pod.uuid` | POD 稳定身份 |
| `inst_id` | `pod.inst_id` | CMDB 数字 ID，用于数字引用解析和来源追溯；不作为图身份 |
| `basic_code` | `pod.basic_code` | POD 基础编码 |
| `name` | `pod.name` | POD 展示和检索名称 |
| `full_name` | `pod.full_name` | POD 完整展示名称 |
| `plane` | `pod.plane` | 平面数值枚举，也是设备归属关系的规范平面值 |
| `mode` | `pod.mode` | 网络模式原值，允许为空 |
| `rdma` | `pod.rdma` | RDMA 能力原值 |
| `phy_building_id` | `pod.phy_building_id` | 来源物理楼宇数字 ID；当前只保存，不建立楼宇节点 |
| `idc_logic_id` | `pod.idc_logic_id` | 来源逻辑 IDC 数字 ID；当前只保存，不建立逻辑 IDC 关系 |
| `is_delete` | `pod.is_delete` | 来源删除状态，字段缺失时写 `NULL` |
| `is_sync` | `pod.is_sync` | 来源同步状态，字段缺失时写 `NULL` |
| `source_created_at` | `pod.created_at` 或 `pod.create_time` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `pod.updated_at` 或 `pod.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 平面

`plane` 保留来源数值：

| 值 | 含义 |
|---|---|
| `1` | 管理面 |
| `2` | 计算面 |
| `3` | 存储面 |
| `4` | 带外管理面 |

未知非空数值保持原值并记录未知枚举诊断，不自行映射为其他平面。

#### 名称

节点保存来源 `basic_code`、`name` 和 `full_name`。展示名称按以下顺序选择，不额外保存派生字段：

```text
name 非空 → name
否则 full_name 非空 → full_name
否则 basic_code 非空 → basic_code
否则 → uuid
```

名称变化不影响 POD 身份。

#### 来源时间

- `source_created_at`：优先使用非空 `created_at`，否则使用 `create_time`。
- `source_updated_at`：优先使用非空且不是默认占位值的 `updated_at`，否则使用 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`，不得覆盖有效时间。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

#### 数字 ID

- `inst_id`、`phy_building_id`、`idc_logic_id` 按来源整数保存。
- `0` 是否表示未分配按字段分别处理；当前 POD 样例中的有效 ID 均为正整数。
- `inst_id` 必须在同一 `source_id` 下唯一映射到一个 POD UUID。
- 数字 ID 变化不得创建新的 POD 节点；UUID 不变时更新节点属性。

### 3.4 当前不保存字段

以下字段不进入 POD 节点：

- `obj_id`
- `id`
- `action_id`
- `describe`
- `creator`
- `modifier`
- `username`
- `supplier_account`
- `lables`
- `support_ids`
- `support_service_ids`
- `deleted_at`

其中 `lables` 在样例中同时出现数组和数字两种类型，且当前不参与身份、关系或查询，因此不进入目标模型。

## 4. 设备归属关系

### 4.1 管理面 POD

设备管理面来源字段：

```text
device.pod_uuid
device.pod_id
device.pod_name
device.plane
```

解析规则：

1. `pod_uuid` 非空时直接作为目标 POD 稳定身份。
2. 同时存在 `pod_id` 时，必须对应目标 POD 的 `inst_id`。
3. 同时存在 `pod_name` 时，用于校验目标 POD 的 `name`，不参与身份判断。
4. 设备 `plane="管理面"` 时，目标 `pod.plane` 应为 `1`。
5. `pod_uuid` 为空但 `pod_id` 有效时，按 `pod.inst_id` 查询 UUID。
6. UUID 与数字 ID 指向不同 POD 时标记身份冲突，不静默选择其一。

### 4.2 计算面 POD

计算面逐项读取 `device.compute_plane[]`：

```text
compute_plane[].pod_id
compute_plane[].pod_name
compute_plane[].building_id
```

解析规则：

1. 每个非空 `pod_id` 按 `pod.inst_id` 查询 POD UUID，不能只处理数组第一项。
2. 解析后的目标 POD `plane` 应为 `2`。
3. `compute_plane[].pod_name` 与 `pod.name` 用于辅助校验。
4. `compute_plane[].building_id` 与 `pod.phy_building_id` 用于辅助校验。
5. `compute_pod_id[]` 和 `compute_pod_name[]` 只用于完整性核对，不替代 `compute_plane[]` 明细。
6. `compute_plane[]` 当前没有独立 `plane` 字段，关系平面值必须取解析后的 `pod.plane`，不能读取不存在的 `compute_plane[].plane`。

### 4.3 关系生成

管理面和计算面均生成：

```text
device(device_sn) -[spatial_relation {relation_kind: "member_of"}]-> pod(uuid)
```

关系规则：

- `plane` 使用目标 `pod.plane` 的数值，不保存设备侧中文平面名称。
- 同一设备、同一 POD、同一平面被多个来源字段重复引用时，只生成一条关系。
- `relation_id` 由 `Edge Type`、`relation_kind`、两端完整逻辑身份和 `plane` 确定性生成。
- 若同一设备与同一 POD 出现不同平面引用，按冲突处理，不生成猜测关系。
- 未分配 POD 的设备允许没有 `member_of` 关系。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `spatial_relation` | `member_of` | `device(device_sn) → pod(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | `plane` | 管理面 `pod_uuid/pod_id` 或计算面 `compute_plane[].pod_id` |

当前不生成以下关系：

- `pod → data_center`
- `pod → building`
- `pod → logic_idc`
- `compute_plane → pod`

`pod.idc_logic_id` 与设备 `logic_idc_id` 的现有样例不一致，不能据此推导 POD 到数据中心或逻辑 IDC 的关系。

## 6. 同步与清理约定

1. POD 节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 POD，再解析设备管理面和计算面引用。
4. 数字 `pod_id` 通过 `pod.inst_id` 解析 UUID；可使用本轮已采集 POD 数据的临时映射，也可按 `inst_id` 查询 API，但临时映射不写入 MySQL。
5. 设备引用为空、目标不存在、匹配不唯一或辅助身份字段冲突时不生成关系，并记录同步诊断。
6. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
7. 本轮见到的 POD 节点和成功解析的 `member_of` 关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
8. 任一 POD 分页、必要设备分页、关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
9. 当前设备仍引用但无法解析的 POD 不得据此删除旧 `member_of` 关系或 POD 节点；无法安全判断范围时跳过清理。
10. 完整成功后先清理过期 `member_of` 关系，再删除无其他有效引用的过期 POD 节点。
11. 单个 `plane` 过滤查询不是全量同步，不能清理其他平面的节点或关系。
12. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的 POD 快照或关系候选。

## 7. 样例闭环

### 7.1 管理面

设备样例：

```text
pod_uuid = 5fffffb0-ecf0-6623-97f2-00ebe4d84212
pod_id   = 914
pod_name = POD006
plane    = 管理面
```

POD 样例：

```text
uuid    = 5fffffb0-ecf0-6623-97f2-00ebe4d84212
inst_id = 914
name    = POD006
plane   = 1
```

UUID、数字 ID、名称和平面可以完成交叉验证。

### 7.2 计算面

GPU 服务器设备样例：

```text
compute_plane[].pod_id   = 2289
compute_plane[].pod_name = POD246
compute_plane[].building_id = 1036
```

POD 样例：

```text
uuid            = 764f199c-c1e9-44f0-b948-82719f516a09
inst_id         = 2289
name            = POD246
plane           = 2
phy_building_id = 1036
```

数字 ID、名称、平面和楼宇数字 ID 可以完成计算面引用校验，最终关系终点使用 POD UUID。

## 8. 来源证据

- [space_pod.md](../../cmdb/space_pod.md)
- [device_view_demo.json](../../cmdb/device_view_demo.json)
- [device_server_gpu.md](../../cmdb/device_server_gpu.md)
- [device.md](device.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
