# Device 实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于网络、电力、空间、智算分析，不复制完整 CMDB 设备档案。

## 1. 设计目标

`device_view` 是 CMDB 全部设备的统一视图。`device` 统一表示服务器、存储设备、网络设备和其他设备，不再拆成多套设备节点。

本项目遵守以下边界：

1. CMDB 是设备完整信息和来源引用的事实源。
2. NebulaGraph 只保存分析拓扑需要的最小节点属性和已经解析成功的边。
3. MySQL 只保存同步任务、进度、发布状态和统计，不保存设备、设备引用或关系候选。
4. 稳定 UUID、设备 SN 等字段可直接作为 CMDB API 查询条件，不要求建立整轮内存索引；同步重试或图重建时重新读取 CMDB。

## 2. 数据处理结构

这里不是三层持久化模型，而是“两类图数据 + 一类关系解析输入”。

| 类别 | 是否持久化 | 位置 | 职责 |
|---|---|---|---|
| 设备节点 | 是 | NebulaGraph | 保存设备身份、分类及直接查询所需的最小属性 |
| 拓扑关系 | 是 | NebulaGraph | 保存解析成功的设备关系及关系自身属性 |
| 关系解析输入 | 否 | 当前 API 响应或请求流程 | 使用稳定 UUID/SN 直接关联或查询；只有数字独占引用才临时查询目标稳定身份 |

MySQL 可以保存 `run_id`、同步状态、页码或检查点、记录数量、错误统计、发布代次和就绪状态，但不得保存以下设备事实：

- `inst_id`
- `device_uuid`
- `cabinet_uuid`
- POD 引用
- `server_tor_ports`
- 未解析关系明细
- 原始 `device_view` 响应

目标引用暂时无法解析时，本轮不生成对应图边；同步结果只记录必要的错误类型和数量。后续重试重新读取 CMDB 并重新解析，不在 MySQL 中维护设备关系候选。

## 3. Device 图节点

### 3.1 当前必须存储的字段

| 字段 | 必要性 |
|---|---|
| `scope_id` | 隔离租户或拓扑范围 |
| `source_id` | 标识设备来源，避免不同 CMDB 来源之间发生身份碰撞 |
| `device_sn` | 所有设备统一使用的唯一、永久稳定且不复用的最终身份 |
| `name` | 设备展示和检索名称 |
| `parent_type_id` | 设备大类，是网络、电力、空间和智算分析的基础筛选条件 |
| `device_type_id` | 设备子类型，用于区分 GPU 服务器、通用服务器、交换机等 |
| `role` | 网络设备角色，例如 T0/T1；非网络设备允许为空 |
| `all_ips[]` | 规范化、去重后的有效设备 IP，用于按 IP 定位设备 |
| `created_at` | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 本轮完整同步最后见到时间，用于安全清理旧节点 |

逻辑设备身份：

```text
source_id:device:device_sn
```

图 VID 的最终编码还需统一考虑 `scope_id` 和长度限制，本文件不单独固化格式。

### 3.2 名称生成

节点只保存一个规范化 `name`，不同时保存多个展示名称：

```text
device_name 非空 → device_name
否则 host_name 非空 → host_name
否则 → device_sn
```

`device_name` 和 `host_name` 只在同步时读取；生成 `name` 后不再保存。

### 3.3 类型分类

类型来源：[dict_device_type.md](../../cmdb/dict_device_type.md)。

大类：

| `parent_type_id` | 大类 |
|---|---|
| `52` | 服务器 |
| `53` | 存储设备 |
| `54` | 网络设备 |
| `55` | 其他设备 |

子类型由 `device_type_id` 表示，例如：

- `57`：通用服务器
- `58`：GPU 服务器
- `190`：盒式交换机

当前同时保存 `parent_type_id` 和 `device_type_id`：

- `parent_type_id` 用于高频大类筛选。
- `device_type_id` 用于具体拓扑能力和子类型判断。
- `parent_type`、`device_type` 名称不进设备节点，展示时通过字典解析。
- 同步时校验 `device_type_id` 对应字典项的 `pid` 等于 `parent_type_id`。

### 3.4 地址处理

`device_ip_info` 是地址主来源；顶层 `eth_ip`、`ilo_ip`、`management_ip` 只用于补全和校验。

当前只将有效 IP 规范化、去重后写入 `all_ips[]`。以下详细信息暂不保存：

- `gateway`
- `mask`
- `mac`
- `port`
- `vlanid`
- 地址来源类型和用途

IP 不是设备身份，也不建立 IP 图节点。

## 4. 关系解析输入

本节字段只用于读取来源和解析关系，不写入 MySQL，也不作为设备节点属性。API 调用中的局部变量不等于同步状态；能够用稳定 UUID/SN 重新查询的数字 ID 或别名不保留。

### 4.1 身份与查询

`device_sn` 是设备最终身份，可直接确定设备 VID。

- 来源已提供 `device_sn` 时直接使用。
- 来源只提供稳定 `device_uuid` 时，以该 UUID 查询设备 API 取得 `device_sn`，不预存 `device_uuid → device_sn` 索引。
- 只有来源仅提供数字 ID、没有稳定 UUID/SN 时，才在当前请求处理中以 `inst_id` 查询稳定身份。

已确认 `device_view.inst_id = device_id`；需要数字查询时只使用其中一个字段。`inst_id`、`device_id`、`device_uuid` 均不进入目标模型。`obj_id` 固定为 `device_view`，不保存；顶层 `uuid` 会变化，不参与身份和建边。

### 4.2 设备到机柜

使用稳定 `device_view.cabinet_uuid` 直接生成：

```text
device(device_sn) → cabinet(uuid)
```

`cabinet_uuid` 是当前响应中的关系输入，不复制到设备节点，也不要求另建内存映射。当前不读取或保存以下非必要字段：

- `idc_cabinet_id`
- `cabinet`
- `u_position`
- `u_start`
- `u_end`

当前空间拓扑只表达设备位于哪个机柜，不表达设备占用的 U 位。

### 4.3 设备到 POD

管理面优先使用稳定 UUID：

```text
device_sn → pod_uuid
edge.plane = resolved pod.plane
```

同时使用 `pod_id`、`pod_name` 和设备侧中文 `plane` 校验目标 POD。

计算面逐项读取 `compute_plane[]`。当前来源只提供 `pod_id`，因此按 `pod.inst_id` 查询 POD UUID：

```text
device_sn → pod(uuid resolved by compute_plane[].pod_id)
edge.plane = resolved pod.plane
```

`compute_plane[]` 当前没有独立 `plane` 字段，不能读取 `compute_plane[].plane`。`pod_id`、`pod_name`、`compute_pod_id[]` 和 `compute_pod_name[]` 等来源引用不保存到设备节点；完整规则见 [pod.md](pod.md)。

### 4.4 普通服务器 ToR 上联

从每个 `server_tor_ports[]` 项取得 ToR SN 和端口名：

```text
source_device_sn = 当前服务器 device_sn
target_device_sn = server_tor_ports[].sn
target_port_name = server_tor_ports[].ports[]
```

按 `target_device_sn + target_port_name` 直接查询或匹配 `port_view.local_device_sn + port_name`，取得稳定 `port_uuid`。具体接口约定见 [interface.md](interface.md)。

不建立 `(ToR SN, port_name) → port_uuid` 整轮内存索引。当前建边只使用 `sn` 和 `ports[]`；`server_tor_sn`、`ip`、`ip_type`、`as_number` 不保存。

新样例 [device_server_tor_demo.md](../../cmdb/device_server_tor_demo.md) 已验证：

| ToR `device_sn` | 端口名 | `port_uuid` |
|---|---|---|
| `21980114493GN1002115` | `25GE1/0/26` | `f00eabc6-b5fc-15e8-efcd-96a26f0498ee` |
| `21980114493GN1001855` | `25GE1/0/26` | `fc93c428-a2b1-1cd3-cc94-720b85456c2c` |

引用这些精确 SN 和端口名的普通服务器上联可以解析。其他引用按 CMDB API 实际查询结果解析；无法解析时不生成边。

## 5. 拓扑关系

设备参与的当前最小关系如下：

| Edge Type | `relation_kind` | 端点 | 必要边属性 | 来源 |
|---|---|---|---|---|
| `spatial_relation` | `located_in` | `device(device_sn) → cabinet(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `device_view.cabinet_uuid` |
| `spatial_relation` | `member_of` | `device(device_sn) → pod(uuid)` | `plane`、`relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 管理面 POD 或计算面 POD 引用 |
| `composition_relation` | `owns_interface` | `device(device_sn) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 详见 [interface.md](interface.md) |
| `network_relation` | `server_uplink` | `device(device_sn) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `server_tor_ports[].sn + ports[]` |
| `composition_relation` | `contains_gpu` | `device(device_sn) → gpu(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `server_gpu.device_sn` |

`relation_id` 优先使用带 `scope_id/source_id` 限定的来源关系 UUID；没有来源关系 UUID 时，由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。`member_of` 允许业务属性参与唯一性，其关系 ID 还必须包含规范化 `plane`。

说明：

- 关系端点和必要关系属性必须存，因为它们就是分析拓扑本身。
- 普通服务器来源没有服务器本端接口 UUID，因此 `server_uplink` 直接指向 ToR 接口，不伪造服务器接口节点。
- 网络设备端口归属由 `port_view` 生成，不把端口列表复制到设备节点。
- 电力影响路径从 `device → cabinet` 开始，再沿机柜、RPP、UPS 和变压器关系遍历；设备自身无需保存功耗字段。
- GPU 卡及 GPU 上联分别由 GPU 实体和 GPU 关系定义，不把 GPU 汇总复制到设备节点。

## 6. 当前明确不存储的字段

- `device_view.uuid`
- `inst_id`、`device_id`、`device_uuid`：不进入目标模型；稳定 UUID/SN 直接查询，只有数字独占引用才在当前请求处理中使用 `inst_id`
- `obj_id`
- `service_status` / `service_status_id`
- `operation`、`asset` 等资产状态
- `cabinet_uuid`、POD 引用、`server_tor_ports`：只作为当前响应中的关系解析输入，不复制到设备节点或同步状态
- `u_position`、`u_start`、`u_end`
- `power`、`rated_power` 及其他设备功耗字段
- 厂商、型号和配置
- 资产编号、部门、业务、应用和负责人
- 操作系统、固件、维保和采购信息
- 网关、掩码、MAC、VLAN、地址用途
- 楼栋、房间、模组和逻辑 IDC 的设备冗余引用
- GPU 汇总字段
- ToR 引用中的 `ip`、`ip_type`、`as_number`

## 7. 同步与校验规则

1. 设备按 `device_sn` 去重；`device_sn` 必须非空，并在同一 `source_id` 下唯一。
2. 关系来源提供稳定 UUID/SN 时直接查询或关联，不建立 `inst_id → device_sn`、`device_uuid → device_sn` 等整轮内存索引。
3. 只有数字独占引用才按目标对象类型使用 `inst_id` 查询稳定身份；数字 ID 不进入节点、关系或同步状态。
4. `device_type_id` 的字典父 ID 必须等于记录的 `parent_type_id`。
5. 引用为空或无法唯一解析时不生成图边，并计入本轮同步诊断。
6. 只有完整分页采集、关系解析和图写入成功后，才能发布本轮设备拓扑。
7. 本轮所有设备节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
8. 完整同步成功后才能清理 `synced_at<T` 的受管旧关系和旧节点；先清关系，再清节点。
9. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的设备快照、关系候选或临时别名索引。

## 8. 来源证据

| 来源 | 用途 |
|---|---|
| [device_view.md](../../cmdb/device_view.md) | 设备接口字段说明 |
| [device_view_demo.json](../../cmdb/device_view_demo.json) | 服务器、网络设备及普通服务器 ToR 引用样例 |
| [dict_device_type.md](../../cmdb/dict_device_type.md) | 设备大类和子类型字典 |
| [device_network_port.md](../../cmdb/device_network_port.md) | 网络设备别名和端口归属规则 |
| [device_server_tor_demo.md](../../cmdb/device_server_tor_demo.md) | 普通服务器 ToR 设备与端口闭环样例 |
| [device_server_gpu.md](../../cmdb/device_server_gpu.md) | GPU 服务器与计算面 POD 引用样例 |
| [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md) | 跨实体拓扑关系和同步边界 |
