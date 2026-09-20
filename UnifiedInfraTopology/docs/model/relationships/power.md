# 电力关系模型

状态：已确认采用领域统一 Edge Type。

## 1. 建模结论

电力领域统一使用一个 Edge Type：

```text
power_relation
```

具体电力语义由 `relation_kind` 区分：

```text
power_upstream
member_of
has_standby
```

这些关系均来自 CMDB。`power_relation` 统一领域查询入口，但三种 `relation_kind` 的业务含义不能互换：

- `power_upstream`：供电主链。
- `member_of`：UPS 与 UPS 组的成员关系。
- `has_standby`：候选备用配置。

## 2. 端点矩阵

| `relation_kind` | 起点 | 终点 | 来源解析 | 业务属性 |
|---|---|---|---|---|
| `power_upstream` | `cabinet(uuid)` | `rpp(uuid)` | `row_switch_id_A/B → rpp.inst_id → rpp.uuid` | `power_path=A/B` |
| `power_upstream` | `rpp(uuid)` | `ups_group(uuid)` | `rpp.ups_group_id → ups_group.inst_id → ups_group.uuid` | 无 |
| `member_of` | `ups(uuid)` | `ups_group(uuid)` | `ups.ups_group_id → ups_group.inst_id → ups_group.uuid` | 无 |
| `power_upstream` | `ups_group(uuid)` | `transformer(uuid)` | `ups_group.transformer_id_up → transformer.inst_id → transformer.uuid` | 无 |
| `has_standby` | `transformer(primary_uuid)` | `transformer(standby_uuid)` | `standby_transformer_id → transformer.inst_id → transformer.uuid` | 无 |

## 3. 关系表达

主供电链统一由下游对象指向上游对象：

```text
cabinet
  -[power_relation {
      relation_kind: "power_upstream",
      power_path: "A|B"
    }]->
rpp
  -[power_relation {relation_kind: "power_upstream"}]->
ups_group
  -[power_relation {relation_kind: "power_upstream"}]->
transformer
```

UPS 是 UPS 组成员，不作为主链中的串联层级：

```text
ups
  -[power_relation {relation_kind: "member_of"}]->
ups_group
```

备用变压器配置：

```text
transformer(primary)
  -[power_relation {relation_kind: "has_standby"}]->
transformer(standby)
```

## 4. 解析规则

### 4.1 机柜连接 RPP

1. `row_switch_id_A` 生成 `power_path=A`。
2. `row_switch_id_B` 生成 `power_path=B`。
3. `power_path` 是 `relation_id` 的业务区分字段，用于区分 A/B 路的逻辑关系身份。
4. A/B 路连接不同 RPP 时，两条边的终点不同，均可使用默认 `rank=0`。
5. A/B 路连接同一 RPP 时，两条边的起点、Edge Type 和终点相同，必须根据各自 `relation_id` 使用稳定且不同的 Rank，避免物理边互相覆盖。
6. 数字引用必须唯一解析到 RPP UUID。
7. 当前 A 路样例已闭环；B 路目标未提供时只记录诊断，不创建占位关系。

### 4.2 RPP 连接 UPS 组

1. 使用 `rpp.ups_group_id → ups_group.inst_id` 解析。
2. 不使用组名、标签或位置猜测目标。
3. RPP 的设备级变压器引用只用于与 UPS 组上游配置校验，不生成跨层捷径。

### 4.3 UPS 属于 UPS 组

1. 使用 `ups.ups_group_id → ups_group.inst_id` 解析。
2. 不使用 `ups_group_id_tag` 建边。
3. 该关系表示设备编组，不表示 UPS 串并联方式、负载分配或冗余等级。
4. UPS 的变压器引用只用于一致性校验，不生成 `ups → transformer`。

### 4.4 UPS 组连接变压器

1. 使用 `ups_group.transformer_id_up → transformer.inst_id` 解析目标 UUID。
2. 这是当前主供电链的最上游关系。
3. 关系表示 CMDB 配置的供电上游，不证明实时供电状态、电流方向或负载分配。

### 4.5 备用变压器

1. 关系方向与来源字段一致：主变压器指向备用变压器。
2. 目标必须唯一解析，并通过自引用和有向环检查。
3. 自引用、目标不唯一或形成循环时不发布关系，并记录同步诊断。
4. `has_standby` 不属于默认供电主链。
5. 该关系只表示候选备用配置，不表示备用对象当前可用、可切换或已经供电。

## 5. 属性

所有 `power_relation` 保存公共属性：

```text
relation_id
relation_kind
scope_id
source_id
created_at
synced_at
```

子类型属性：

| 属性 | 适用 `relation_kind` | 含义 |
|---|---|---|
| `power_path` | `power_upstream` 且端点为 `cabinet → rpp` | A/B 供电路径 |

## 6. 关系身份

无来源关系 UUID 时，确定性生成：

```text
power_relation + relation_kind + 两端完整逻辑身份 + 必要业务区分字段
```

`cabinet → rpp` 必须将 `power_path` 纳入关系身份。

## 7. 查询语义

默认供电链必须逐边限定 `relation_kind="power_upstream"`：

```ngql
MATCH p=(cabinet)-[edges:power_relation*1..3]->(upstream)
WHERE id(cabinet) == $cabinet_vid
  AND ALL(
    edge IN edges
    WHERE edge.power_relation.relation_kind == "power_upstream"
  )
RETURN p;
```

- 查询机柜供电上游：正向遍历 `power_upstream`。
- 查询变压器潜在影响范围：反向遍历 `power_upstream`。
- 查询 UPS 组成员：反向遍历 `member_of`。
- 默认故障影响查询不得包含 `has_standby`。
- 备用评估必须显式查询 `has_standby`，并标明其非实时可用性语义。
- 未过滤 `relation_kind` 的 `power_relation` 多跳可能把成员或备用关系错误纳入主供电路径，禁止用于严格供电分析。

## 8. 一致性校验但不建边

以下来源引用只用于校验，不生成跨层关系：

```text
rpp.transformer_group_a_id ↔ ups_group.transformer_id_up
ups.transformer_id_up      ↔ ups_group.transformer_id_up
```

引用不一致时记录 `conflict`，不静默选择任一目标，也不改变已正确解析的主链关系。

## 9. 延后范围

当前不建立：

- RPP、UPS 组、UPS、变压器到数据中心的直接关系。
- `rpp → transformer` 和 `ups → transformer` 跨层捷径。
- 独立空开、PDU、馈线、电力线路、市电和发电机节点及关系。
- 根据设备数量或容量推断的串并联、冗余、负载和故障切换关系。

## 10. 来源实体

- [cabinet.md](../entities/cabinet.md)
- [rpp.md](../entities/rpp.md)
- [ups_group.md](../entities/ups_group.md)
- [ups.md](../entities/ups.md)
- [transformer.md](../entities/transformer.md)
