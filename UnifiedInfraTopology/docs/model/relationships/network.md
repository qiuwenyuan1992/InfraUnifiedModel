# 网络关系模型

状态：已确认采用领域统一 Edge Type。

## 1. 建模结论

网络领域统一使用一个 Edge Type：

```text
network_relation
```

具体网络语义由 `relation_kind` 区分：

```text
server_uplink
gpu_uplink
links_to
```

三类关系都是 CMDB 提供的网络连接事实。统一 Edge Type 不是规则派生，也不生成第二套分析边；它只把原来的网络关系名称收敛为 `network_relation.relation_kind`。

当前模型有意省略普通服务器本端 NIC/HCA/接口。省略中间资源不改变 CMDB 上联事实，但必须在文档和查询结果中明确抽象层级。

## 2. 端点矩阵

| `relation_kind` | 起点 | 终点 | 来源解析 | 关系特性 |
|---|---|---|---|---|
| `server_uplink` | `device(device_sn)` | `interface(port_uuid)` | `server_tor_ports[].sn + ports[] → port_view.local_device_sn + port_name` | 普通服务器不创建本端接口节点 |
| `gpu_uplink` | `gpu(uuid)` | `interface(port_uuid)` | 以 `device_sn + gpu_sn` 解析 GPU，以 `tor_sn + tor_port` 解析 ToR 端口 | 来源具有稳定关系 UUID |
| `links_to` | `interface(port_uuid)` | `interface(port_uuid)` | `port_uuid + remote_interface_uuid` | 规范化单向存储，业务双向遍历 |

## 3. 关系表达

```text
device(server)
  -[network_relation {relation_kind: "server_uplink"}]->
interface(ToR)

gpu
  -[network_relation {relation_kind: "gpu_uplink"}]->
interface(ToR)

interface
  -[network_relation {relation_kind: "links_to"}]->
interface
```

关系存在只表示 CMDB 记录的连接或上联配置，不直接证明端口在线、协议已建立、链路健康或端到端实时可达。

## 4. 解析规则

### 4.1 普通服务器上联

1. 使用 `server_tor_ports[].sn` 定位 ToR 网络设备。
2. 使用 `server_tor_ports[].ports[]` 与 `port_view.port_name` 匹配端口。
3. 解析成功后使用 ToR 端口 UUID 建边。
4. 当前不为普通服务器创建本端接口节点，因此关系直接从服务器指向 ToR 端口。
5. SN 与端口名无法唯一解析时不建边，并记录同步诊断。

### 4.2 GPU 上联

解析链：

```text
(gpu_uplink.device_sn, gpu_uplink.gpu_sn)
  → GPU
(gpu_uplink.tor_sn, gpu_uplink.tor_port)
  → ToR interface
```

规则：

1. 来源 `gpu_uplink.uuid` 是稳定关系身份。
2. `gpu_port` 表示 GPU 关联网卡端口名，但当前不创建网卡或网卡端口节点。
3. 可保存已确认属性：`gpu_port`、`gpu_port_speed`、`gpu_ip`、`gpu_slot`、`server_port_speed`、`bond_name`、`tor_port`、`tor_port_speed`、`tor_role`、`source`。
4. GPU 或 ToR 端口无法唯一解析时不建边。

### 4.3 网络端口物理连接

业务语义：

```text
interface(A) ↔ interface(B)
```

图中规范化存储：

```text
interface(min_uuid)
  -[network_relation {relation_kind: "links_to"}]->
interface(max_uuid)
```

规则：

1. 来源使用本端 `port_uuid` 和 `remote_interface_uuid`。
2. 两个端口 UUID 按字典序规范化，生成唯一 `relation_id`。
3. 双端 LLDP 记录只生成一条逻辑连接。
4. 对端接口不存在时不创建占位端口或关系，并记录同步诊断。
5. 不同端口对不能因为所属设备相同而合并。
6. 查询时按双向遍历；存储方向不代表流量方向。

## 5. 属性

所有 `network_relation` 保存公共属性：

```text
relation_id
relation_kind
source_id
created_at
synced_at
```

网络领域属性采用同一 Schema；不适用于某个 `relation_kind` 的字段为空。

| 属性 | 主要适用 `relation_kind` |
|---|---|
| `gpu_port`、`gpu_port_speed`、`gpu_ip`、`gpu_slot` | `gpu_uplink` |
| `server_port_speed`、`bond_name` | `gpu_uplink` |
| `tor_port`、`tor_port_speed`、`tor_role`、`source` | `gpu_uplink` |

后续若引入通用链路速率、状态或网络平面属性，应定义为网络领域公共属性，不再增加新的网络 Edge Type。

## 6. 关系身份

来源提供稳定关系 UUID 时，优先使用来源身份。当前 GPU 上联关系为：

```text
source_id:network_relation:gpu_uplink:uuid
```

其他网络关系确定性生成：

```text
network_relation + relation_kind + 两端完整逻辑身份 + 必要业务区分字段
```

`links_to` 必须先规范化端点顺序，再生成关系身份。

## 7. 查询语义

```ngql
-- 查询资源的网络上联
MATCH (resource)-[edge:network_relation]->(interface:interface)
WHERE id(resource) == $resource_vid
  AND edge.network_relation.relation_kind IN ["server_uplink", "gpu_uplink"]
RETURN edge, interface;
```

```ngql
-- 查询接口物理邻居
MATCH (source:interface)-[edge:network_relation]-(target:interface)
WHERE id(source) == $interface_vid
  AND edge.network_relation.relation_kind == "links_to"
RETURN target;
```

- 服务器或 GPU 上联查询沿关系正向遍历。
- 从 ToR 端口查询下联资源时反向遍历上联关系。
- 接口物理邻接对 `relation_kind="links_to"` 双向遍历。
- 未过滤 `relation_kind` 的查询适合网络领域全景探索，不适合精确链路判断。
- 设备级网络路径需要结合 `composition_relation(relation_kind="owns_interface")` 还原接口所属设备；这属于原始事实组合查询，不新增派生边。

## 8. 延后范围

当前不建立：

- 普通服务器本端接口节点。
- 网卡、HCA 和网卡端口节点。
- 端口组节点。
- 缺少明确成员资料的 LAG `aggregates` 关系。
- IP 地址节点和地址拥有关系。
- 聚合链路、逻辑可达性或实时健康状态派生边。

## 9. 来源实体与样例

- [device.md](../entities/device.md)
- [interface.md](../entities/interface.md)
- [gpu.md](../entities/gpu.md)
- [device_network_lldp_demo_data.md](../../cmdb/device_network_lldp_demo_data.md)
