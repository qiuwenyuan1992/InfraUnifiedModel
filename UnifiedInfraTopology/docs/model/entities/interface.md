# Interface 实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于网络连接、服务器上联和 GPU 上联分析，不复制完整 `port_view` 记录。

## 1. 设计目标

`interface` 表示 `port_view` 中具有稳定 `port_uuid` 的网络设备端口。当前不为缺少稳定端口身份的普通服务器或 GPU 关联网卡虚构接口节点。

本实体遵守以下边界：

1. CMDB API 是端口完整信息和来源引用的事实源。
2. NebulaGraph 只保存拓扑查询需要的最小端口属性和已解析成功的关系。
3. MySQL 只保存同步任务、进度、发布状态和统计，不保存端口快照、来源数字 ID 或未解析关系候选。
4. 稳定 UUID、设备 SN 等字段可以直接作为 CMDB API 查询条件，不要求建立整轮内存索引。
5. 只有来源仅提供数字引用且没有稳定 UUID/SN 时，才在当前请求处理中使用数字 ID 查询目标稳定身份；数字 ID 不成为同步状态或图属性。

## 2. Interface 图节点

### 2.1 身份

接口最终身份使用：

```text
port_uuid
```

当前样例中：

```text
port_uuid = local_interface_uuid
```

最终只使用 `port_uuid` 建立接口节点。`port_view.uuid` 是视图记录 UUID，不作为接口身份，也不保存。

逻辑接口身份：

```text
source_id:interface:port_uuid
```

### 2.2 当前保存字段

| 字段 | 来源 | 必要性 |
|---|---|---|
| `scope_id` | 同步上下文 | 隔离租户或拓扑范围 |
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `port_uuid` | `port_view.port_uuid` | 接口稳定身份 |
| `port_name` | `port_view.port_name` | 展示及按设备范围匹配端口 |
| `port_type` | `port_view.port_type` | 按 CMDB 原值保存：`physics` 或 `virtual` |
| `port_speed` | `port_view.port_speed` | 保存来源原值，不自行换算单位 |
| `port_operation_status` | `port_view.port_operation_status` | 保存来源原值，不自行解释枚举 |
| `group_uuid` | `port_view.group_uuid` | 端口组链路稳定 UUID |
| `port_group_uuid` | `port_view.port_group_uuid` | 本端端口组成员稳定 UUID |
| `group_name` | `port_view.port_group_name` | 端口组展示名称，不参与身份判断 |
| `group_type` | `port_view.group_type` | 一级分类原始编号 |
| `port_group_type` | `port_view.port_group_type` | 二级分类原始编号 |
| `group_subtype` | `port_view.group_subtype` | 三级分类原始编号 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

端口组空值规范：

- `group_uuid`、`port_group_uuid`、`group_name` 的空字符串写为 `NULL`。
- `group_type`、`port_group_type`、`group_subtype` 的来源值 `0` 写为 `NULL`。
- 上述值均为 `NULL` 表示端口未加入端口组。
- 有效分类编号保持 CMDB 原值，不自行转换或合并。

### 2.3 当前不保存字段

以下字段不进入接口节点：

- `port_view.uuid`
- `inst_id`
- `port_id`
- `local_interface_id`
- `device_uuid`
- `local_device_uuid`
- `local_device_id`
- `local_device_sn`
- `remote_interface_uuid`
- `remote_interface_id`
- `remote_device_uuid`
- `remote_device_id`
- `remote_device_sn`
- `group_id`
- `port_group_id`
- `local_if_index`
- `snmp_ifindex`
- `rack_uuid`
- `room_uuid`
- `local_pod_uuid`
- `local_logic_idc_uuid`

其中设备和对端字段只作为关系解析输入；关系解析完成后由图边表达，不复制到接口节点。`local_if_index` 和 `snmp_ifindex` 当前不参与身份、归属、LLDP/CMDB 连线或服务器/GPU 上联，后续接入 SNMP 指标时再评估。

## 3. 关系解析

### 3.1 网络设备拥有接口

优先使用来源已有的稳定设备 SN：

```text
port_view.local_device_sn → device.device_sn
port_view.port_uuid        → interface.port_uuid
```

生成：

```text
device(device_sn) -[owns_interface]-> interface(port_uuid)
```

如果来源缺少 `local_device_sn`，但提供稳定 `local_device_uuid`，则直接以该 UUID 查询设备 API 取得 `device_sn`。不要求预存 `device_uuid → device_sn` 内存索引，也不使用 `local_device_id` 进行必要交叉校验。

### 3.2 接口物理连接

对端存在稳定接口 UUID 时：

```text
port_view.port_uuid             → 本端 interface
port_view.remote_interface_uuid → 对端 interface
```

生成：

```text
interface(port_uuid) -[links_to]-> interface(remote_interface_uuid)
```

规则：

- `remote_interface_uuid` 为空时，只保存本端接口，不生成连接边。
- 对端 UUID 查询不到时，本轮不生成边并记录同步诊断，不保存未解析数字 ID 或别名。
- 若记录 A 与记录 B 同时满足 `A.port_uuid = B.remote_interface_uuid` 且 `A.remote_interface_uuid = B.port_uuid`，则确认形成双端互指。
- 两端重复上报同一端口对时合并为一条逻辑连接，不因两条 `port_view` 记录具有不同来源 `uuid` 而生成两条边。
- `links_to` 的端点顺序按两个端口 UUID 规范化；推荐按 UUID 字典序将较小值作为起点、较大值作为终点，查询时按双向遍历处理。
- 双端互指时，可进一步核对两侧 `local_device_uuid` 与对侧 `remote_device_uuid`、端口数字 ID、端口名称、速率和角色是否对称；这些字段不替代端口 UUID 身份。
- 不同端口对不能因为所属设备相同而合并。
- CMDB 连接信息表示来源关系，不直接等同实时可达状态。

### 3.3 普通服务器上联

普通服务器来源没有服务器本端端口 UUID。使用：

```text
server_tor_ports[].sn      → port_view.local_device_sn
server_tor_ports[].ports[] → port_view.port_name
```

直接查询或匹配得到 `port_uuid`，生成：

```text
device(server_device_sn) -[server_uplink]-> interface(tor_port_uuid)
```

不要求保存 `(ToR SN, port_name) → port_uuid` 内存索引，也不建立服务器本端接口节点。

### 3.4 GPU 上联

GPU 上联使用 `tor_sn + tor_port` 直接查询或匹配 ToR 接口：

```text
(gpu_uplink.tor_sn, gpu_uplink.tor_port)
    → (port_view.local_device_sn, port_view.port_name)
    → port_uuid
```

解析成功后生成：

```text
gpu(uuid) -[gpu_uplink]-> interface(port_uuid)
```

`ib8` 等 GPU 侧端口名不是具有稳定 UUID 的网络设备端口，当前不据此创建接口节点。

## 4. 端口组查询

端口已有 `port_group_uuid` 时，直接调用 `port_group` API：

```json
{
  "condition": {
    "uuid": "bd30c202-4f2f-579e-64a5-5527e5d4d815"
  }
}
```

查询规则：

1. 使用稳定 `port_group_uuid`，不使用或保存 `port_group_id`。
2. 返回记录中的 `port_group_link_id` 只在当前请求流程中用于继续查询同一链路的全部成员。
3. `port_group_link_id` 不进入接口节点、不写入 MySQL，也不作为整轮同步内存状态。
4. 因 API 支持灵活条件查询，无需为了后续调用预存 `group_id`。
5. 三种一级端口组类型统一使用 `port_group` API，不按类型拆分接口。

## 5. 同步约定

1. 先按 `port_uuid` 去重接口节点。
2. 关系来源提供稳定 UUID/SN 时，直接查询或关联目标。
3. API 调用过程中的局部变量不等于需要保存的同步状态；能从稳定身份重新查询得到的数字 ID 或别名不保留。
4. 只有数字独占引用才按目标对象类型使用 `inst_id` 查询稳定身份。
5. 引用为空或无法唯一解析时不生成关系，并计入本轮同步诊断。
6. 只有完整分页采集、关系解析和图写入成功后，才能发布本轮接口拓扑。
7. 本轮所有接口节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。完整成功后才能清理 `synced_at<T` 的受管旧关系和旧节点。
8. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的端口快照、关系候选或临时别名索引。

## 6. 拓扑关系

| 关系 | 端点 | 必要边属性 | 来源 |
|---|---|---|---|
| `owns_interface` | `device(device_sn) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `port_view.local_device_sn`，缺失时按 `local_device_uuid` 查询 |
| `links_to` | `interface(port_uuid) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `remote_interface_uuid` |
| `server_uplink` | `device(device_sn) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `server_tor_ports[].sn + ports[]` |
| `gpu_uplink` | `gpu(uuid) → interface(port_uuid)` | GPU 上联业务属性、`relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `gpu_uplink.tor_sn + tor_port` |

除 `links_to` 外，`relation_id` 优先使用明确的来源关系 UUID；没有来源关系 UUID 时，由关系类型和两端稳定身份确定性生成。

`links_to` 不使用 `port_view.uuid` 作为关系身份，因为同一物理连接的双端记录具有不同来源 UUID。其 `relation_id` 固定由 `scope_id`、`source_id`、关系类型和规范化后的端口 UUID 对确定性生成，避免双端重复上报产生两条逻辑连接。

## 7. LLDP 双端样例闭环

补充样例已验证以下两条记录互为对端：

```text
端口 A
port_uuid              = 542866bc-936b-49b8-8d6d-5a531fc1bb69
port_id                = 11576397
local_device_uuid      = 795fa397-3a73-7d42-d435-467c78b2b311
port_name              = HundredGigabitEthernet2/21
remote_interface_uuid  = ba7e98e2-3514-0b8a-63bc-df7eca91428d
remote_interface_id    = 5211713
remote_device_uuid     = e39ae9aa-9bbd-1170-4790-5f6c0ddd7f89

端口 B
port_uuid              = ba7e98e2-3514-0b8a-63bc-df7eca91428d
port_id                = 5211713
local_device_uuid      = e39ae9aa-9bbd-1170-4790-5f6c0ddd7f89
port_name              = 100GE1/0/5
remote_interface_uuid  = 542866bc-936b-49b8-8d6d-5a531fc1bb69
remote_interface_id    = 11576397
remote_device_uuid     = 795fa397-3a73-7d42-d435-467c78b2b311
```

闭环结论：

1. `A.port_uuid = B.remote_interface_uuid`。
2. `A.remote_interface_uuid = B.port_uuid`。
3. 两侧本端设备 UUID 分别等于对侧记录的远端设备 UUID。
4. 两侧端口数字 ID 也互相对应，但只用于交叉核对，不作为最终关系身份。
5. 两侧端口速率均为 `100000`；角色分别为 `T1-T0` 与 `T0-T1`，符合对称上报特征。
6. 两条来源记录 UUID `5dd45f63-e18b-4452-95c7-3c7b564298bf` 和 `b565ad10-1e50-446c-b5f1-91dad0e49651` 不同，但只生成一条规范化逻辑关系：

```text
interface(542866bc-936b-49b8-8d6d-5a531fc1bb69)
  -[links_to]->
interface(ba7e98e2-3514-0b8a-63bc-df7eca91428d)
```

该方向仅由端口 UUID 字典序确定；业务查询按双向连接处理。

## 8. 来源证据

- [device_network_port.md](../../cmdb/device_network_port.md)
- [device_network_lldp_demo_data.md](../../cmdb/device_network_lldp_demo_data.md)
- [device_netwok_group.md](../../cmdb/device_netwok_group.md)
- [device_server_tor_demo.md](../../cmdb/device_server_tor_demo.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
