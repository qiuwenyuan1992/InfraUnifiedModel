# 机柜实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于表达设备物理位置、机柜 POD 标签、数据中心归属及 A/B 路供电入口。

## 1. 设计目标

`cabinet` 表示 CMDB `cabinet` 对象。本阶段采用“仅拓扑最小”模型，不承载容量、运行状态、电流、PDU、租赁、组织或业务运营信息。

本实体遵守以下边界：

1. 机柜节点以 `cabinet.uuid` 作为最终身份。
2. 按已确认要求保留机柜自身及空间、POD、供电来源数字 ID，用于 API 查询、关系解析和来源追溯。
3. `idc_id` 与 `phy_idc_id` 语义冗余；目标模型只保留并使用 `idc_id`，完全丢弃 `phy_idc_id`。
4. `pod_id` 没有业务意义，完全丢弃；只使用 `pod_ids[]` 表达机柜 POD 标签。
5. 机柜 POD 标签不代表机柜内所有设备都属于该 POD，不能据此生成或修改 `device → pod` 关系。
6. 当前建立 `device → cabinet`、`cabinet → data_center`、`cabinet → pod` 和 A/B 路 `cabinet → rpp` 关系。
7. 当前不建立楼宇、房间、模组或机柜列节点。
8. NebulaGraph 保存机柜节点和已解析成功的关系；MySQL 不保存机柜快照或未解析关系候选。

## 2. 机柜查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `cabinet`：

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

只有全部分页成功后，该来源和范围的机柜节点集合才可视为完整。

### 2.2 按数据中心查询

接口支持按 `idc_id` 查询机柜：

```json
{
  "condition": {
    "idc_id": 451
  }
}
```

按单个 IDC 查询可作为明确的数据中心同步范围，但只允许清理该 `idc_id` 覆盖的机柜及关系，不能据此清理其他数据中心的数据。

### 2.3 按其他空间数字 ID 查询

接口样例还支持按以下字段过滤：

```text
idc_module_id
phy_room_id
```

这些局部查询用于排障、核对或受限范围同步。除非同步任务明确声明并完整覆盖该范围，否则不能作为全量清理依据。

## 3. 机柜图节点

### 3.1 身份

机柜最终身份使用：

```text
cabinet.uuid
```

逻辑机柜身份：

```text
scope_id:source_id:cabinet:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `dc_colo_rack`
- `isp_rack_code`
- `rack`
- `idc_id`
- `idc_rows_code`

机柜编码和位置名称可能变化，不能参与永久身份判断。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `scope_id` | 同步上下文 | 隔离租户或拓扑范围 |
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `cabinet.uuid` | 机柜稳定身份 |
| `inst_id` | `cabinet.inst_id` | CMDB 数字 ID，用于查询和来源追溯；不作为图身份 |
| `code` | `cabinet.code` | 机柜编码和主要展示名称 |
| `dc_colo_rack` | `cabinet.dc_colo_rack` | 建筑、房间和机架组合展示值 |
| `isp_rack_code` | `cabinet.isp_rack_code` | 运营商机柜编码 |
| `rack` | `cabinet.rack` | 来源机架名称 |
| `rack_column_code` | `cabinet.rack_column_code` | 机柜列展示编码 |
| `rack_row` | `cabinet.rack_row` | 机柜排展示值，允许为空 |
| `idc_id` | `cabinet.idc_id` | 数据中心数字引用，用于解析 `data_center.inst_id` |
| `phy_building_id` | `cabinet.phy_building_id` | 物理楼宇数字 ID；当前不建立楼宇节点 |
| `phy_room_id` | `cabinet.phy_room_id` | 物理房间数字 ID；当前不建立房间节点 |
| `idc_module_id` | `cabinet.idc_module_id` | 模组数字 ID；当前不建立模组节点 |
| `idc_building_structure_id` | `cabinet.idc_building_structure_id` | 来源楼宇结构数字 ID；当前只保存 |
| `idc_rows_code` | `cabinet.idc_rows_code` | 机柜列数字编码；当前不建立机柜列节点 |
| `pod_ids[]` | `cabinet.pod_ids` | 机柜 POD 标签数字 ID 列表，用于解析 `pod.inst_id` |
| `row_switch_id_a` | `cabinet.row_switch_id_A` | A 路列头柜数字引用，用于解析 `rpp.inst_id` |
| `row_switch_id_b` | `cabinet.row_switch_id_B` | B 路列头柜数字引用，用于解析 `rpp.inst_id` |
| `source_created_at` | `cabinet.created_at` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `cabinet.updated_at` 或 `cabinet.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值和数字引用

- 空字符串统一写为 `NULL`。
- `pod_ids[]` 过滤 `NULL`、`0` 和重复值后保存；空列表写为空数组。
- `idc_id`、空间数字 ID 和 `row_switch_id_a/b` 按来源整数保存。
- 数字引用为 `0`、空值或非法值时视为未分配，不生成关系。
- 数字 ID 变化不创建新机柜节点；UUID 不变时更新节点属性及相应关系。

#### 编码和展示名称

节点保存来源 `code`、`dc_colo_rack`、`isp_rack_code`、`rack`、`rack_column_code` 和 `rack_row`。展示名称按以下顺序选择，不额外保存派生字段：

```text
code 非空 → code
否则 dc_colo_rack 非空 → dc_colo_rack
否则 isp_rack_code 非空 → isp_rack_code
否则 → uuid
```

编码变化不影响机柜身份。

#### 数据中心引用

- 只保存并使用 `idc_id`。
- `phy_idc_id` 不进入节点、不用于关系解析，也不作为交叉校验字段。
- `idc_id` 通过 `data_center.inst_id` 解析目标 UUID。
- `idc_id` 为空或无法唯一解析时，不生成 `cabinet → data_center` 关系。

#### POD 标签

- `pod_id` 完全丢弃，不保存、不校验、不参与关系解析。
- `pod_ids[]` 是机柜 POD 标签的唯一来源字段。
- 每个 POD 数字 ID 通过 `pod.inst_id` 解析目标 UUID。
- 多个标签必须全部处理，不能只取第一项。
- 标签列表只说明机柜被哪些 POD 标记，不表示机柜内设备的实际逻辑归属。

#### 来源时间

- `source_created_at` 使用非空 `created_at`。
- `source_updated_at` 优先使用非空且不是默认占位值的 `updated_at`，否则使用 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

### 3.4 当前不保存字段

以下字段不进入机柜节点：

- `phy_idc_id`
- `pod_id`
- `obj_id`
- `id`
- `action_id`
- `basic_code`
- `building_full_name`
- `room_full_name`
- `device_num`
- `server_num`
- `network_device_num`
- `gpu_device_num`
- `external_num`
- `other_num`
- `u_num`
- `spare_u_num`
- `used_u_num`
- `rack_size`
- `status`
- `ops_status`
- `is_delete`
- `is_sync`
- `usage_type`
- `power_status`
- `power_state`
- `power_type`
- `rated_current`
- `isp_rated_current`
- `maximum_current`
- `max_current`
- `allow_use_current`
- `tec_max_current`
- `remain_power`
- `used_rated_power`
- `pdu_num`
- `rest_a_pdu`
- `rest_b_pdu`
- `outlets_10a`
- `outlets_16a`
- `10a_outlets`
- `16a_outlets`
- `rack_tor_fix`
- `rack_tor_inter_speed`
- `rack_tor_type`
- `jira_key`
- `order_issue`
- 租赁、采购、合同、费用、组织、业务标签、支持列表及人员字段

统计数量不能替代图中实际设备关系计数。容量、电流和状态字段后续只有在出现明确查询需求时，才通过显式模型变更加入。

## 4. 关系解析

### 4.1 设备位于机柜

设备来源字段：

```text
device_view.cabinet_uuid
```

解析成功后生成：

```text
device(device_sn) -[spatial_relation {relation_kind: "located_in"}]-> cabinet(uuid)
```

规则：

1. `cabinet_uuid` 直接匹配 `cabinet.uuid`。
2. 不使用机柜名称、编码或数字 ID 猜测目标机柜。
3. `cabinet_uuid` 为空表示设备当前没有已确认机柜位置，允许缺少关系。
4. 目标机柜不存在时不创建占位节点或关系，并记录同步诊断。
5. 设备搬迁时，新的 `cabinet_uuid` 生成新关系；完整成功后清理旧位置关系。

### 4.2 机柜属于数据中心

使用：

```text
cabinet.idc_id → data_center.inst_id → data_center.uuid
```

解析成功后生成：

```text
cabinet(uuid) -[spatial_relation {relation_kind: "located_in"}]-> data_center(uuid)
```

规则：

1. `idc_id` 必须唯一解析到一个数据中心 UUID。
2. 不使用 `phy_idc_id`。
3. 不使用名称、地址或机柜编码推导数据中心。
4. 当前只表达机柜到数据中心的直接归属，不展开楼宇、房间和模组层级。

### 4.3 机柜 POD 标签

逐项解析：

```text
cabinet.pod_ids[] → pod.inst_id → pod.uuid
```

生成：

```text
cabinet(uuid) -[spatial_relation {relation_kind: "tagged_with"}]-> pod(uuid)
```

规则：

1. 过滤空值、`0` 和重复 ID 后逐项解析。
2. `pod_id` 不参与任何处理。
3. 同一机柜与同一 POD 只生成一条标签关系。
4. 无法解析或匹配不唯一时不生成关系，并记录机柜 UUID、POD 数字 ID 和错误类型。
5. 该关系不能用于推导 `device → pod`；设备实际 POD 归属仍以设备来源字段为准。
6. 可将机柜标签与柜内设备的 POD 归属做一致性分析，但差异只作为诊断，不自动修改任何关系。

### 4.4 A/B 路供电入口

分别解析：

```text
cabinet.row_switch_id_A → rpp.inst_id → rpp.uuid
cabinet.row_switch_id_B → rpp.inst_id → rpp.uuid
```

生成：

```text
cabinet(uuid) -[power_relation {relation_kind: "power_upstream", power_path: "A"}]-> rpp(uuid)
cabinet(uuid) -[power_relation {relation_kind: "power_upstream", power_path: "B"}]-> rpp(uuid)
```

规则：

1. A、B 路独立解析，任一路失败不影响另一条已成功关系。
2. `power_path` 必须保存为规范值 `A` 或 `B`。
3. 若 A、B 路引用同一 RPP，仍保留两条关系，关系身份必须包含 `power_path`。
4. 引用为空、为 `0`、目标不存在或匹配不唯一时，不生成对应路径关系。
5. 该关系表示 CMDB 配置的上游供电引用，不证明实时通电状态、电流方向或切换状态。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `spatial_relation` | `located_in` | `device(device_sn) → cabinet(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `device_view.cabinet_uuid` |
| `spatial_relation` | `located_in` | `cabinet(uuid) → data_center(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `cabinet.idc_id` |
| `spatial_relation` | `tagged_with` | `cabinet(uuid) → pod(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `cabinet.pod_ids[]` |
| `power_relation` | `power_upstream` | `cabinet(uuid) → rpp(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `power_path` | `row_switch_id_A/B` |

关系身份规则：

- 上述来源均没有独立关系 UUID。
- `located_in` 和 `tagged_with` 的 `relation_id` 由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。
- `power_upstream` 的 `relation_id` 由 `Edge Type`、`relation_kind`、两端完整逻辑身份和 `power_path` 确定性生成。
- 两端完整逻辑身份必须包含 `scope_id`、`source_id`、对象类型和稳定身份。

## 6. 同步与清理约定

1. 机柜节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 `data_center`、`pod`、`rpp` 和 `cabinet`，再解析机柜关系；设备位置关系在设备数据可用后解析。
4. `idc_id`、`pod_ids[]` 和 `row_switch_id_A/B` 可通过本轮已采集对象的临时 `inst_id → UUID` 映射解析，也可按 `inst_id` 查询对应 API；临时映射不写入 MySQL。
5. 机柜节点保存已确认的数字来源引用，但这些字段不替代关系，也不作为图端点身份。
6. 引用为空、目标不存在、匹配不唯一或身份冲突时不生成关系，并记录对象类型、机柜 UUID、来源字段、引用值和错误类型。
7. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
8. 本轮见到的机柜节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
9. 任一必要分页、关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
10. 当前设备、POD 标签、数据中心或供电引用仍存在但无法解析时，不得据此删除可能对应的旧关系；无法安全判断范围时跳过该机柜范围清理。
11. 完整成功后先清理过期 `located_in`、`tagged_with` 和 `power_upstream` 关系，再删除无其他有效引用的过期机柜节点。
12. 按单个 `idc_id`、房间或模组过滤的查询只能清理其明确覆盖范围，不能清理范围外机柜。
13. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的机柜快照或关系候选。

## 7. 样例闭环

当前机柜样例：

```text
uuid                      = c0b7b4f5-c5b2-ab3d-af4e-da3fbe7c055b
inst_id                   = 62897
code                      = M1-F2-IT06-R08-16
idc_id                    = 451
phy_building_id           = 404
phy_room_id               = 889
idc_module_id             = 100
idc_building_structure_id = 1728
idc_rows_code             = 15756
pod_ids                   = [914]
row_switch_id_A           = 6204
row_switch_id_B           = 6114
```

已验证：

1. `device_view_demo.json` 中 6 台设备的 `cabinet_uuid` 均匹配该机柜 UUID。
2. `idc_id=451` 可匹配 `data_center.inst_id=451`，目标 UUID 为 `0e32c4d6-a29f-4d8b-94c8-38ac791e363d`。
3. `pod_ids=[914]` 可匹配 `pod.inst_id=914`，目标 UUID 为 `5fffffb0-ecf0-6623-97f2-00ebe4d84212`。
4. A 路 `row_switch_id_A=6204` 可匹配 RPP UUID `9f283fc3-d029-4a4d-b312-d23b5c2c4cd5`。
5. B 路 `row_switch_id_B=6114` 当前未提供目标 RPP 样例，因此只确认引用存在；同步时按同样规则查询，未解析前不生成 B 路关系。

## 8. 来源证据

- [space_cainet.md](../../cmdb/space_cainet.md)
- [space_idc.md](../../cmdb/space_idc.md)
- [space_pod.md](../../cmdb/space_pod.md)
- [space_rpp.md](../../cmdb/space_rpp.md)
- [device_view_demo.json](../../cmdb/device_view_demo.json)
- [device.md](device.md)
- [pod.md](pod.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
