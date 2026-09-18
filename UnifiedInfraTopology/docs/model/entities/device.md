# Device 实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-18 已确认的 CMDB 规则和样例整理；用于网络、电力、空间、智算分析，不复制完整 CMDB 设备档案。

## 1. 设计目标

`device_view` 是 CMDB 全部设备的统一视图。`device` 统一表示服务器、存储设备、网络设备和其他设备，不再拆成多套设备节点。

当前阶段只保存：

1. 图查询和设备识别必须使用的节点属性。
2. 构造、重试和核对关系必须使用的来源引用。
3. 已解析关系及其必要属性。

其他 CMDB 字段列为后续扩展，不进入当前模型。

## 2. 三层存储模型

设备数据分为三层，不能简单理解为“全部存到设备节点”或“建完边就全部丢弃”。

| 层次 | 存储位置 | 职责 |
|---|---|---|
| 第一层：设备节点 | NebulaGraph `device` 节点 | 保存设备身份、分类及直接查询所需的最小属性 |
| 第二层：同步解析数据 | MySQL 身份绑定、关系候选及解析诊断 | 保存来源别名和建边引用，支持跨接口解析、失败重试、冲突诊断和全量重建 |
| 第三层：拓扑关系 | NebulaGraph 边 | 保存成功解析后的设备关系及关系自身属性 |

规则：

- 关系来源字段不等于设备节点属性。
- 已成功建边后，关系来源仍应保留在第二层的当前关系候选中，供重建和核对使用。
- 未解析或冲突的关系候选只保存在第二层，不伪造图端点。
- 原始 API JSON 可选作短期审计存档，但不能替代结构化的身份绑定和关系候选。

## 3. 第一层：设备节点

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

`device_name` 和 `host_name` 可在采集时读取，但生成 `name` 后不作为当前设备节点属性保存。

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
- 同步时必须校验 `device_type_id` 对应字典项的 `pid` 等于 `parent_type_id`。

### 3.4 地址处理

`device_ip_info` 是地址主来源；顶层 `eth_ip`、`ilo_ip`、`management_ip` 只用于补全和校验。

当前只将有效 IP 规范化、去重后写入 `all_ips[]`。以下详细信息暂不进入设备节点：

- `gateway`
- `mask`
- `mac`
- `port`
- `vlanid`
- 地址来源类型和用途

精确 IP 查询可由 MySQL 维护 `IP → source_id/device_sn` 索引。IP 不是设备身份，也不建立 IP 图节点。

## 4. 第二层：同步解析数据

第二层数据必须持久化，但不作为设备图节点属性。

### 4.1 身份别名

| 字段 | 保存规则 | 用途 |
|---|---|---|
| `inst_id` | 所有设备保存 | CMDB 查询、数字引用解析和身份交叉校验 |
| `device_uuid` | 仅网络设备非空时保存 | 将 `port_view.local_device_uuid` 解析为设备 `device_sn` |

已确认 `device_view.inst_id = device_id`，因此只保存 `inst_id`，不重复保存 `device_id`。

`obj_id` 固定为 `device_view`，由采集器和来源类型确定，不作为每台设备的属性重复保存。

`device_view` 顶层 `uuid` 会变化，不进入设备节点、身份绑定或关系候选；如需审计，只能存在原始响应存档中。

身份索引至少包括：

```text
(source_id, device_sn)   → device VID
(source_id, inst_id)     → device_sn
(source_id, device_uuid) → network device_sn
```

### 4.2 关系候选公共字段

每条候选关系至少保存：

- `scope_id`
- `source_id`
- `relation_kind`
- 来源设备 `device_sn`
- 规范化目标引用
- `resolution_status`：`resolved`、`unresolved`、`conflict`
- 失败或冲突原因
- 已解析的目标稳定身份
- `synced_at`

只有 `resolved` 候选能够发布到图。

### 4.3 设备到机柜候选

从 `device_view.cabinet_uuid` 生成：

```text
device_sn → cabinet_uuid
```

当前不保存：

- `idc_cabinet_id`
- `cabinet`
- `u_position`
- `u_start`
- `u_end`

这些字段不是当前拓扑建边的必要条件。当前空间拓扑只表达设备位于哪个机柜，不表达设备占用的 U 位。

### 4.4 设备到 POD 候选

管理面来源：

```text
device_sn → pod_uuid
edge.plane = plane
```

计算面来源：逐项读取 `compute_plane[]`，优先使用其中的 `pod_id` 解析 POD：

```text
device_sn → pod(inst_id = compute_plane[].pod_id)
edge.plane = compute_plane[].plane
```

第二层只保存规范化后的逐条 POD 候选，不必把完整 `compute_plane[]`、`compute_pod_id[]`、`compute_pod_name[]` 复制到设备节点。

`pod_id`、`pod_name` 等重复字段只在采集时用于校验；关系候选形成后不重复保存无必要副本。

### 4.5 普通服务器 ToR 上联候选

从每个 `server_tor_ports[]` 项拆成“一台服务器、一个 ToR、一个端口”的候选关系：

```text
source_device_sn = 当前服务器 device_sn
target_device_sn = server_tor_ports[].sn
target_port_name = server_tor_ports[].ports[]
```

解析方式：

```text
(target_device_sn, target_port_name) → port_view.port_uuid
```

当前建边只需要 `sn` 和 `ports[]`。以下字段暂不保存：

- `server_tor_sn`：它是 `server_tor_ports[].sn` 的重复汇总字段
- `ip`
- `ip_type`
- `as_number`

新样例 [device_server_tor_demo.md](../../cmdb/device_server_tor_demo.md) 已验证：

| ToR `device_sn` | 端口名 | `port_uuid` |
|---|---|---|
| `21980114493GN1002115` | `25GE1/0/26` | `f00eabc6-b5fc-15e8-efcd-96a26f0498ee` |
| `21980114493GN1001855` | `25GE1/0/26` | `fc93c428-a2b1-1cd3-cc94-720b85456c2c` |

因此，引用这两个精确 SN 和端口名的普通服务器上联可以解析，不再统一标记为 `unresolved`。其他 SN 或端口仍按实际解析结果处理。

## 5. 第三层：拓扑关系

设备参与的当前最小关系如下：

| 关系 | 端点 | 必要边属性 | 来源 |
|---|---|---|---|
| `located_in` | `device(device_sn) → cabinet(uuid)` | `scope_id`、`source_id`、`synced_at` | `device_view.cabinet_uuid` |
| `member_of` | `device(device_sn) → pod(uuid)` | `plane`、`scope_id`、`source_id`、`synced_at` | 管理面 POD 或计算面 POD 候选 |
| `owns_interface` | `device(device_sn) → interface(port_uuid)` | `scope_id`、`source_id`、`synced_at` | `port_view` 的本端设备引用 |
| `server_uplink` | `device(device_sn) → interface(port_uuid)` | `scope_id`、`source_id`、`synced_at` | `server_tor_ports[].sn + ports[]` |
| `contains_gpu` | `device(device_sn) → gpu(uuid)` | `scope_id`、`source_id`、`synced_at` | `server_gpu.device_sn` |

说明：

- 普通服务器来源没有服务器本端接口 UUID，因此 `server_uplink` 直接指向 ToR 接口，不伪造服务器接口节点。
- 网络设备端口归属主要由 `port_view` 生成，不把端口列表复制到设备节点。
- 电力影响路径从 `device → cabinet` 开始，再沿机柜、RPP、UPS 和变压器关系遍历；设备自身无需保存功耗字段。
- GPU 卡及 GPU 上联分别由 GPU 实体和 GPU 关系文档定义，不把 GPU 汇总复制到设备节点。

## 6. 当前明确不存储的字段

### 6.1 不进入任何结构化当前模型

- `device_view.uuid`
- `device_id`，因为已确认等于 `inst_id`
- `obj_id`，因为固定为 `device_view`
- `service_status` / `service_status_id`
- `operation`、`asset` 等资产状态
- `u_position`、`u_start`、`u_end`
- `power`、`rated_power` 及其他设备功耗字段

### 6.2 后续扩展字段

以下字段当前不需要，只有出现明确分析需求后再扩展：

- 厂商、型号和配置：`manufacturer`、`model`、`configure`
- 资产编号、部门、业务、应用和负责人
- 操作系统、固件、维保和采购信息
- 设备详细地址属性：网关、掩码、MAC、VLAN、地址用途
- 楼栋、房间、模组和逻辑 IDC 的设备冗余引用
- GPU 汇总：`parts_gpu`、`pkg_gpu_count`、`pkg_gpu_manufacturer`、`pkg_gpu_model` 等
- ToR 候选中的 `ip`、`ip_type`、`as_number`
- 设备功耗和容量规格

## 7. 最小校验规则

1. `device_sn` 必须非空，并在同一 `source_id` 下唯一。
2. 同一 `device_sn` 不得对应多个 `inst_id`。
3. 同一 `inst_id` 不得对应多个 `device_sn`。
4. 网络设备非空 `device_uuid` 必须唯一映射到一个 `device_sn`。
5. `device_type_id` 的字典父 ID 必须等于记录的 `parent_type_id`。
6. 关系候选无法唯一解析时不发布图边，保留 `unresolved` 或 `conflict` 诊断。
7. 完整同步成功后才能按 `synced_at` 清理旧关系和旧节点；先清关系，再清节点。

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
