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
| `power_upstream` | `cabinet(uuid)` | `rpp(inst_id)` | `row_switch_id_A/B → rpp.inst_id` | `power_path=A/B` |
| `power_upstream` | `rpp(inst_id)` | `ups_group(inst_id)` | `rpp.ups_group_id → ups_group.inst_id` | 无 |
| `member_of` | `ups(inst_id)` | `ups_group(inst_id)` | `ups.ups_group_id → ups_group.inst_id` | 无 |
| `power_upstream` | `ups_group(inst_id)` | `transformer(inst_id)` | `ups_group.transformer_id_up → transformer.inst_id` | 无 |
| `has_standby` | `transformer(primary_inst_id)` | `transformer(standby_inst_id)` | `standby_transformer_id → transformer.inst_id` | 无 |

四类电力实体的权威身份为 `inst_id`，图属性为 `INT NOT NULL`。同一 `source_id`、同一目标实体类型下，数字引用直接匹配目标 `inst_id`；不建立 `inst_id → UUID` 映射，不生成或要求电力实体 UUID，也不以编码、标签或名称作为身份。目标存在且唯一后，使用其正整数 `inst_id` 的规范十进制字符串作为 `StableID`，结合来源和类型生成哈希 VID；机柜端点仍使用自身 UUID。

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
6. 数字引用必须在同一 `source_id` 下唯一匹配 RPP `inst_id`。
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

1. 使用 `ups_group.transformer_id_up → transformer.inst_id` 直接匹配同一 `source_id` 下的目标稳定身份。
2. 这是当前主供电链的最上游关系。
3. 关系表示 CMDB 配置的供电上游，不证明实时供电状态、电流方向或负载分配。

### 4.5 备用变压器

1. 关系方向与来源字段一致：主变压器指向备用变压器。
2. 目标必须唯一解析；允许两台及多台变压器构成备用组或循环引用，按来源保存所有方向的 `has_standby`，不合并或任选方向。
3. 自引用、目标缺失或不唯一时不发布关系，并记录同步诊断。备用循环仅为配置特征，不因数量或成环拒绝；验收仍检查每条边的身份、rank、端点与来源，不把备用配置当作主供电流向。
4. `has_standby` 不属于默认供电主链。
5. 该关系只表示候选备用配置，不表示备用对象当前可用、可切换或已经供电。

## 5. 属性

所有 `power_relation` 保存公共属性：

```text
relation_id
relation_kind
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

两端完整逻辑身份使用 `[source_id, entity_type, stable_id]`；电力实体的 `stable_id` 为规范正十进制 `inst_id` 字符串，例如 `["cmdb-primary","rpp","6204"]`，机柜的 `stable_id` 仍为 UUID。不得将裸数字引用或名称直接作为关系身份；VID 按 `SHA-256(["vertex", source_id, entity_type, stable_id])` 确定性生成。

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
