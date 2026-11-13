# ROOM 房间模型

状态：已批准。来源证据为 `UnifiedInfrastructureModel/evidence/数据中心房间API.md` 与同名 JSON。

## 身份与来源

- Tag：`room`。完整稳定身份为 `source_id + room + uuid`；UUID 必填且去除首尾空白。
- CMDB `inst_id` 必须是正整数 INT，仅用于解析来源引用，不能代替 UUID、生成 UUID 或作为回退身份。
- POST `/pub/api/v3/find/instance/object/room`，按 `inst_id` 升序有界分页采集；生产同步只完整读取一次来源。
- `room_condition` 默认 `status: 1`，可显式配置状态；配置只含 `idc_id` 时仍默认状态 1。空间同步必须选定完整 IDC，状态固定为 1，不接受冲突 IDC 或其他窄化条件。

## 图属性

| 属性 | 类型 | 约束 |
|---|---|---|
| `source_id` | STRING | 必填 |
| `uuid` | STRING | 必填，稳定身份 |
| `inst_id` | INT | 来源正整数，引用解析 |
| `full_name` | STRING | 来源编码名称 |
| `room_full_name` | STRING | 来源完整显示名称，展示优先 |
| `name` | STRING | 来源短名称 |
| `idc_id` | INT | 数据中心来源数字引用 |
| `idc_module_id` | INT | 保留来源模组引用，不建模组节点 |
| `phy_building_id` | INT | 保留楼宇引用，不建楼宇节点 |
| `status` | INT | 来源状态 |
| `cabinet_num` | INT | 来源机柜计数快照，仅目录展示，不替代图关系 |
| `server_num` | INT | 来源服务器计数快照，仅目录展示 |
| `network_device_num` | INT | 来源网络设备计数快照，仅目录展示 |
| `source_created_at` | DATETIME | `created_at` 来源时间 |
| `source_updated_at` | DATETIME | `updated_at`，为空时使用 `last_time` |
| `created_at` | DATETIME | 图首次创建时间，必填 |
| `synced_at` | DATETIME | 当前完整同步时间，必填 |

## 空间关系

- `room.idc_id → data_center.inst_id → data_center.uuid`，生成 ROOM 到数据中心的 `located_in`。
- `cabinet.phy_room_id → room.inst_id → room.uuid`，生成机柜到 ROOM 的 `located_in`。
- `rpp.idc_room → room.inst_id → room.uuid`，生成 RPP 到 ROOM 的 `located_in`；RPP 仍以正整数 `inst_id` 为稳定身份。
- 先完整读取所选 IDC 的 ROOM 目录；数字 ID 或 UUID 冲突均诊断为 `ambiguous_room`，缺失为 `missing_room`，空引用为 `empty_room_reference`，不伪造节点或身份。
- 保留设备机柜、机柜数据中心和机柜 POD 标签关系。不得推导 ROOM 子房间树、楼宇树或其他未证实层级。

## 迁移边界

最新 `0001_topology.ngql` 是新图基线；旧图单独审阅、手动执行 `0005_room.ngql`，两者不得重复应用 RPP ADD。
既有 `cabinet.phy_room_id INT NULL` 保留；增量仅创建 ROOM Tag/身份索引并新增 `rpp.idc_room INT NULL`。
不创建 SQL 资产表，不在服务启动时执行图 DDL。
