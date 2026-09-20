# 资源组成关系模型

状态：已确认采用领域统一 Edge Type。

## 1. 建模结论

资源组成领域统一使用一个 Edge Type：

```text
composition_relation
```

具体组成语义由 `relation_kind` 区分：

```text
owns_interface
contains_gpu
```

这些关系均来自 CMDB。统一 Edge Type 不改变资源归属事实，也不把网络连通、空间归属或供电路径混入组成领域。

## 2. 端点矩阵

| `relation_kind` | 起点 | 终点 | 来源解析 | 适用对象 |
|---|---|---|---|---|
| `owns_interface` | `device(device_sn)` | `interface(port_uuid)` | 优先使用 `port_view.local_device_sn`；缺失时按 `local_device_uuid` 查询设备并取得 `device_sn` | 网络设备 |
| `contains_gpu` | `device(device_sn)` | `gpu(uuid)` | `server_gpu.device_sn → device.device_sn` | GPU 服务器 |

## 3. 关系表达

```text
device
  -[composition_relation {relation_kind: "owns_interface"}]->
interface

device
  -[composition_relation {relation_kind: "contains_gpu"}]->
gpu
```

## 4. 解析规则

### 4.1 网络设备拥有接口

1. 设备最终身份使用 `device_sn`，接口最终身份使用 `port_uuid`。
2. 优先按 `local_device_sn` 关联设备。
3. 来源缺少 SN 但具有稳定 `local_device_uuid` 时，查询设备 API 取得 `device_sn`。
4. 不使用数字 `local_device_id` 作为最终端点身份。
5. 普通服务器当前不创建本端接口节点，因此不生成服务器接口的组成关系。
6. 设备端口变化时按完整成功的同步结果更新关系。

### 4.2 服务器包含 GPU

1. 使用 `server_gpu.device_sn = device.device_sn` 关联。
2. GPU 最终身份使用 `server_gpu.uuid`，不使用 GPU 序列号替代 UUID。
3. 同一设备与 GPU 只保存一条组成关系。
4. GPU 更换服务器不改变 GPU 身份；完整同步成功后更新所属关系。
5. `contains_gpu` 不表示 GPU 网络上联；网络连接由 `network_relation` 表达。

## 5. 属性

所有 `composition_relation` 保存：

```text
relation_id
relation_kind
scope_id
source_id
created_at
synced_at
```

当前两个子类型不需要额外业务属性。新增组成子类型时，应先扩展合法端点矩阵，再决定是否增加领域属性。

## 6. 关系身份

当前来源没有独立关系 UUID，`relation_id` 按以下内容确定性生成：

```text
composition_relation + relation_kind + 拥有者完整逻辑身份 + 子资源完整逻辑身份
```

不得使用设备名称、端口名称、GPU 插槽或数字对象 ID 作为关系最终身份。

## 7. 查询语义

```ngql
-- 查询设备全部已建模组成资源
MATCH (device)-[edge:composition_relation]->(resource)
WHERE id(device) == $device_vid
RETURN edge.composition_relation.relation_kind, resource;
```

精确查询应同时限定目标 Tag 和 `relation_kind`：

```ngql
-- 查询服务器 GPU
MATCH (device)-[edge:composition_relation]->(gpu:gpu)
WHERE id(device) == $device_vid
  AND edge.composition_relation.relation_kind == "contains_gpu"
RETURN gpu;
```

- 查询设备端口：正向遍历 `relation_kind="owns_interface"`。
- 查询端口所属设备：反向遍历 `relation_kind="owns_interface"`。
- 查询服务器 GPU：正向遍历 `relation_kind="contains_gpu"`。
- 查询 GPU 所属服务器：反向遍历 `relation_kind="contains_gpu"`。

## 8. 延后范围

当前不建立：

- 普通服务器本端端口节点和拥有关系。
- 网卡、HCA、网卡端口及其组成关系。
- 端口组节点及成员关系。
- CPU、内存、磁盘等其他服务器部件节点和关系。

## 9. 来源实体

- [device.md](../entities/device.md)
- [interface.md](../entities/interface.md)
- [gpu.md](../entities/gpu.md)
