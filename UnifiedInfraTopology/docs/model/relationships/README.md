# 实体关系模型总览

状态：正式采用 4 个领域 Edge Type。本文是当前关系模型结论，不再保留 10 类、8 类等候选方案。

## 1. 最终结论

关系模型按业务领域统一为 4 个 Edge Type：

```text
spatial_relation
composition_relation
network_relation
power_relation
```

统一原则：

1. **Edge Type 表示关系所属领域。**
2. **`relation_kind` 表示领域内的具体业务语义。**
3. **端点 Tag、方向和 `relation_kind` 共同确定关系含义。**
4. **所有当前关系均来自 CMDB；字段解析成图边属于规范化入库，不属于规则派生。**
5. **不建立第二套重复的分析边。**只有未来新增聚合链路、动态可达性或算法快照时，才单独定义派生模型。
6. **领域内可以统一遍历，精确业务查询必须过滤 `relation_kind`。**
7. **不同领域不合并为全局 `infra_relation`**，避免网络、空间、组成和供电路径被无条件混合遍历。

当前模型包含：

- **4 个 Edge Type**。
- **11 个领域内关系语义项**，其中 `member_of` 分别存在于空间领域和电力领域。
- **14 个合法端点组合**。

`relation_kind` 只在所属 Edge Type 内解释，不要求全局唯一。例如：

```text
spatial_relation.relation_kind = "member_of"
power_relation.relation_kind   = "member_of"
```

前者表示设备属于 POD，后者表示 UPS 属于 UPS 组，两者不会混淆。

## 2. 领域导航

| Edge Type | 文档 | `relation_kind` | 主要用途 |
|---|---|---|---|
| `spatial_relation` | [spatial.md](spatial.md) | `located_in`、`member_of`、`tagged_with` | 物理位置、逻辑范围和空间标签 |
| `composition_relation` | [composition.md](composition.md) | `owns_interface`、`contains_gpu` | 设备与内部资源组成 |
| `network_relation` | [network.md](network.md) | `server_uplink`、`gpu_uplink`、`links_to` | 服务器/GPU 上联及端口物理连接 |
| `power_relation` | [power.md](power.md) | `power_upstream`、`member_of`、`has_standby` | 供电主链、UPS 成员和备用配置 |

## 3. 完整端点矩阵

### 3.1 空间领域

| Edge Type | `relation_kind` | 端点 | 方向语义 |
|---|---|---|---|
| `spatial_relation` | `member_of` | `device → pod` | 设备属于管理面或计算面 POD |
| `spatial_relation` | `located_in` | `device → cabinet` | 设备位于机柜 |
| `spatial_relation` | `located_in` | `cabinet → data_center` | 机柜位于数据中心 |
| `spatial_relation` | `tagged_with` | `cabinet → pod` | 机柜带有 POD 标签 |

### 3.2 资源组成领域

| Edge Type | `relation_kind` | 端点 | 方向语义 |
|---|---|---|---|
| `composition_relation` | `owns_interface` | `device → interface` | 网络设备拥有端口 |
| `composition_relation` | `contains_gpu` | `device → gpu` | 服务器包含 GPU |

### 3.3 网络领域

| Edge Type | `relation_kind` | 端点 | 方向语义 |
|---|---|---|---|
| `network_relation` | `server_uplink` | `device → interface` | 普通服务器上联到 ToR 端口 |
| `network_relation` | `gpu_uplink` | `gpu → interface` | GPU 上联到 ToR 端口 |
| `network_relation` | `links_to` | `interface → interface` | 两个网络设备端口物理连接 |

### 3.4 电力领域

| Edge Type | `relation_kind` | 端点 | 方向语义 |
|---|---|---|---|
| `power_relation` | `power_upstream` | `cabinet → rpp` | 机柜 A/B 路连接上游 RPP |
| `power_relation` | `power_upstream` | `rpp → ups_group` | RPP 连接上游 UPS 组 |
| `power_relation` | `member_of` | `ups → ups_group` | UPS 属于 UPS 组 |
| `power_relation` | `power_upstream` | `ups_group → transformer` | UPS 组连接主供电上游变压器 |
| `power_relation` | `has_standby` | `transformer(primary) → transformer(standby)` | 主变压器配置备用变压器 |

NebulaGraph 不自动限制 Edge Type 的合法端点组合。同步层必须根据本矩阵校验起点 Tag、终点 Tag、方向和 `relation_kind`。

## 4. 从旧关系名称迁移

原关系名称不再作为独立 Edge Type，而作为领域边的 `relation_kind`：

| 原 Edge Type | 新 Edge Type | `relation_kind` |
|---|---|---|
| `located_in` | `spatial_relation` | `located_in` |
| `member_of`（设备到 POD） | `spatial_relation` | `member_of` |
| `tagged_with` | `spatial_relation` | `tagged_with` |
| `owns_interface` | `composition_relation` | `owns_interface` |
| `contains_gpu` | `composition_relation` | `contains_gpu` |
| `server_uplink` | `network_relation` | `server_uplink` |
| `gpu_uplink` | `network_relation` | `gpu_uplink` |
| `links_to` | `network_relation` | `links_to` |
| `power_upstream` | `power_relation` | `power_upstream` |
| `member_of`（UPS 到 UPS 组） | `power_relation` | `member_of` |
| `has_standby` | `power_relation` | `has_standby` |

该迁移只改变 Schema 表达，不改变：

- CMDB 来源事实。
- 实体稳定身份。
- 关系端点。
- 来源字段解析方式。
- 关系方向。
- 同步完整性要求。

## 5. 公共关系属性

四个 Edge Type 均保存：

| 属性 | 含义 |
|---|---|
| `relation_id` | 关系稳定身份 |
| `relation_kind` | 领域内业务语义，必填 |
| `scope_id` | 租户或拓扑范围 |
| `source_id` | CMDB 来源身份 |
| `created_at` | 本项目首次建边时间，只在首次创建时写入 |
| `synced_at` | 本轮完整同步最后见到时间，用于安全清理 |

领域属性：

| Edge Type | 属性 | 适用范围 |
|---|---|---|
| `spatial_relation` | `plane` | 设备到 POD 的 `member_of` |
| `network_relation` | GPU 上联相关字段 | `gpu_uplink` |
| `power_relation` | `power_path` | 机柜到 RPP 的 `power_upstream` |
| `composition_relation` | 当前无额外属性 | — |

同一领域的子类型共享属性 Schema。不适用于某个 `relation_kind` 的领域属性为空；新增属性前必须确认它属于领域事实，而不是某个临时查询结果。

## 6. 关系身份规则

### 6.1 来源具有稳定关系 UUID

来源明确提供独立、稳定关系 UUID 时，优先使用来源关系身份。当前 GPU 上联为：

```text
scope_id:source_id:network_relation:gpu_uplink:uuid
```

### 6.2 无来源关系 UUID

其他关系确定性生成：

```text
Edge Type + relation_kind + 两端完整逻辑身份 + 必要业务区分字段
```

两端完整逻辑身份包含：

```text
scope_id + source_id + 实体类型 + 稳定实体身份
```

必要业务区分字段包括：

- `spatial_relation/member_of` 使用 `plane` 区分管理面和计算面。
- `power_relation/power_upstream` 的 `cabinet → rpp` 使用 `power_path` 区分 A/B 路。

### 6.3 无向业务语义

`network_relation/links_to` 在业务上双向查询，但图中只保存一个逻辑关系：

1. 将两个端口 UUID 按字典序规范化。
2. 较小 UUID 作为存储起点，较大 UUID 作为终点。
3. 使用规范化端点对生成 `relation_id`。
4. 双端 LLDP 记录只生成一条关系。

### 6.4 NebulaGraph Edge Rank

NebulaGraph 使用以下元组标识一条物理边：

```text
src_vid + Edge Type + Rank + dst_vid
```

`relation_id` 和 `relation_kind` 是边属性，不参与物理边键。Rank 只用于解决同一 Edge Type、同一对端点之间需要保存多条逻辑关系的情况：

1. 同一 `src_vid + Edge Type + dst_vid` 只有一条逻辑关系时，使用默认 `rank=0`。
2. 同一端点之间存在多条逻辑关系时，不能全部使用 `rank=0`；每条关系必须根据 `relation_id` 获得稳定且不同的 Rank。
3. 同一关系在重试、恢复和图重建时必须得到相同 Rank，不得使用写入顺序或临时自增值。
4. 不同 `relation_id` 映射到同一 Rank 时必须作为冲突处理，不得覆盖已有边。
5. Rank 是 NebulaGraph 存储实现细节；业务查询、同步诊断和对外返回仍使用 `relation_id` 与 `relation_kind`。

当前明确需要平行边处理的场景是：机柜 A/B 路同时连接同一 RPP。若 A/B 路连接不同 RPP，则终点不同，两条边均可使用默认 `rank=0`。

## 7. 方向约定

- 空间归属由具体对象指向上级范围：`device → cabinet → data_center`。
- 资源组成由拥有者指向组成部件：`device → interface/gpu`。
- 网络上联由使用者指向 ToR 端口；端口连接规范化单向存储、双向查询。
- 供电主链由下游对象指向上游对象：`cabinet → rpp → ups_group → transformer`。
- 备用关系由主变压器指向备用变压器，但不属于默认供电路径。
- 故障影响范围通常沿供电主链反向遍历。

## 8. 查询规则

### 8.1 领域全景查询

当查询目标就是领域内全部直接关系时，可以只限定 Edge Type：

```ngql
MATCH (source)-[edge:network_relation]-(target)
WHERE id(source) == $vid
RETURN edge, target;
```

### 8.2 精确业务查询

严格查询必须过滤 `relation_kind`，并在需要时校验端点 Tag：

```ngql
MATCH (ups:ups)-[edge:power_relation]->(group:ups_group)
WHERE id(ups) == $ups_vid
  AND edge.power_relation.relation_kind == "member_of"
RETURN group;
```

多跳路径必须保证路径中的每条边都满足目标语义：

```ngql
MATCH p=(cabinet)-[edges:power_relation*1..3]->(upstream)
WHERE id(cabinet) == $cabinet_vid
  AND ALL(
    edge IN edges
    WHERE edge.power_relation.relation_kind == "power_upstream"
  )
RETURN p;
```

### 8.3 跨领域查询

跨领域查询应显式列出参与领域，不建立全局通用边。例如 GPU 到其上联网口：

```text
device
  -[composition_relation {relation_kind: "contains_gpu"}]->
gpu
  -[network_relation {relation_kind: "gpu_uplink"}]->
interface
  <-[composition_relation {relation_kind: "owns_interface"}]-
ToR device
```

这条路径组合的是多条 CMDB 事实，不是派生关系。

## 9. 关系解析与发布

1. 最终端点必须使用实体稳定身份，不直接使用临时数字引用、编码或名称建边。
2. 数字引用必须在同一 `scope_id` 和 `source_id` 下唯一解析到目标稳定身份。
3. 同步层必须校验 Edge Type、`relation_kind`、端点 Tag 和方向符合端点矩阵。
4. 引用为空、目标缺失、匹配不唯一、自引用或违反关系约束时，不创建占位节点或关系，并记录同步诊断。
5. 只有完整采集、身份校验、关系解析和图写入全部成功后，才发布该同步范围。
6. 本轮成功解析的关系刷新 `synced_at=T`。
7. 只有完整成功后才能清理 `synced_at<T` 的受管旧关系；清理顺序为先关系、后节点。
8. 普通排障查询和未声明完整覆盖范围的单对象查询不得执行消失清理。

## 10. 当前闭环状态

已通过样例闭环验证：

- `spatial_relation`：`device → cabinet → data_center`、`device → pod`、`cabinet → pod`。
- `composition_relation`：`device → gpu`、`device → interface`。
- `network_relation`：普通服务器和 GPU 上联、LLDP 双端 `interface ↔ interface`。
- `power_relation`：`cabinet → rpp → ups_group → transformer`、`ups → ups_group`。

因样例目标未提供而尚未闭环，但规则已定义：

- 机柜 B 路 `cabinet → rpp`。
- `transformer → transformer` 的备用关系。

## 11. 明确延后范围

当前不建立：

- 全局 `infra_relation`。
- 与四个事实领域重复的分析边。
- 楼栋、房间、模组、机柜列和逻辑 IDC 节点及关系。
- RPP、UPS 组、UPS、变压器到数据中心的直接关系。
- `rpp → transformer`、`ups → transformer` 等跨层供电捷径。
- PDU、馈线、电力线路、市电和发电机关系。
- 普通服务器本端端口、网卡、HCA、网卡端口和端口组节点及关系。
- 缺少明确成员证据的 LAG 聚合关系。
- 地址节点及地址拥有关系。
- 聚合链路、实时可达性、动态健康状态和算法快照等派生关系。

## 12. 实体定义

实体字段和来源解析细节见 [entities](../entities/) 目录。关系文档只定义跨实体边界、方向、身份、解析、查询和同步语义。
