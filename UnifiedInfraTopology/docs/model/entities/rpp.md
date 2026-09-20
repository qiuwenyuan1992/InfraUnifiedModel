# 列头柜实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于承接机柜 A/B 路供电入口并连接 UPS 组。

## 1. 设计目标

`rpp` 表示 CMDB `idc_RPP` 对象，即列头柜。本阶段采用严格的“拓扑最小”模型，只保存身份、编码、数据中心预留引用、UPS 组引用、变压器一致性校验引用和同步时间。

本实体遵守以下边界：

1. RPP 节点以 `idc_RPP.uuid` 作为最终身份。
2. 保留 `inst_id`，用于解析 `cabinet.row_switch_id_A/B` 和按数字 ID 查询 CMDB；数字 ID 不替代 UUID。
3. 保留 `ups_group`，用于解析并建立 `rpp → ups_group` 的当前供电上游关系。
4. 保留 `transformer_group_a`，但只用于校验经 UPS 组解析出的上游变压器，不建立 `rpp → transformer` 直连关系。
5. 保留来源 `idc`，作为后续版本建立 `rpp → data_center` 关系的入口；当前版本不解析、不建边，也不对其记录关系解析诊断。
6. 当前不保存 `building`、`idc_room`、`module`、`city` 等其他空间字段。
7. 当前不保存回路数量、电源类型、额定电流和额定电压等电气参数。
8. 不根据机柜的 `row_switch_id_A/B` 创建独立“空开”实体；其目标直接是 RPP。
9. NebulaGraph 保存 RPP 节点和已解析成功的当前关系；MySQL 不保存 RPP 快照或未解析关系候选。

## 2. RPP 查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `idc_RPP`：

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

只有全部分页成功后，本轮 RPP 节点集合才可视为完整。

### 2.2 按数字 ID 查询

接口支持按 `inst_id` 精确查询：

```json
{
  "condition": {
    "inst_id": 6204
  }
}
```

该查询用于解析机柜的 `row_switch_id_A/B`、关系排障或单对象核对。单对象查询不构成完整同步范围，不能据此清理其他 RPP 节点或关系。

### 2.3 按 UPS 组查询

接口支持按 `ups_group` 查询下联 RPP：

```json
{
  "condition": {
    "ups_group": 1156
  }
}
```

该查询可用于关系核对或受限范围同步。除非同步任务明确声明并完整覆盖该 UPS 组范围，否则不能作为全量清理依据。

### 2.4 当前不使用的查询条件

接口还支持按 `idc` 和 `module` 查询。当前模型虽然保留 `idc`，但不建立 RPP 数据中心关系；`module` 不进入节点。这些条件只可用于来源排障或受限采集，不能改变当前图关系语义。

## 3. RPP 图节点

### 3.1 身份

RPP 最终身份使用：

```text
rpp.uuid
```

逻辑 RPP 身份：

```text
scope_id:source_id:rpp:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `idc`
- `ups_group`
- `transformer_group_a`

编码、数据中心引用或上游 UPS 组变化时，只更新节点属性及相应关系，不创建新 RPP 节点。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `scope_id` | 同步上下文 | 隔离租户或拓扑范围 |
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `idc_RPP.uuid` | RPP 稳定身份 |
| `inst_id` | `idc_RPP.inst_id` | CMDB 数字 ID，用于机柜引用解析、查询和来源追溯 |
| `code` | `idc_RPP.code` | RPP 编码和主要展示名称 |
| `idc_id` | `idc_RPP.idc` | 数据中心数字引用；仅为后续版本关系预留，当前不解析 |
| `ups_group_id` | `idc_RPP.ups_group` | UPS 组数字引用，用于解析 `ups_group.inst_id` |
| `transformer_group_a_id` | `idc_RPP.transformer_group_a` | 候选变压器数字引用，仅用于上游一致性校验 |
| `source_created_at` | `idc_RPP.create_time` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `idc_RPP.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值和数字引用

- 字符串去除首尾空白后保存，空字符串统一写为 `NULL`。
- `inst_id`、`idc_id`、`ups_group_id` 和 `transformer_group_a_id` 按来源整数保存。
- 数字引用为空、为 `0` 或非法值时统一写为 `NULL`。
- UUID 为空时不能建立 RPP 节点，并记录同步诊断。
- 数字 ID 变化不改变 RPP 身份。

#### 编码和展示名称

节点只保存来源 `code`。展示名称按以下顺序选择，不额外保存派生字段：

```text
code 非空 → code
否则 → uuid
```

`code` 变化不影响 RPP 身份。

#### 数据中心预留引用

- 来源 `idc` 规范保存为节点属性 `idc_id`。
- 潜在映射为：

```text
rpp.idc_id → data_center.inst_id
```

- 当前版本不执行该解析、不建立 `rpp → data_center` 关系。
- 当前版本不因 `idc_id` 为空、目标缺失、匹配不唯一或与其他空间字段不一致而记录关系诊断。
- 后续版本若启用该关系，必须经过独立模型评审，并使用数据中心 UUID 作为最终端点。

#### UPS 组引用

- 来源 `ups_group` 规范保存为 `ups_group_id`。
- `ups_group_id` 通过 `ups_group.inst_id` 解析目标 UUID。
- 无法唯一解析时不生成上游关系，并记录同步诊断。

#### 变压器校验引用

- 来源 `transformer_group_a` 规范保存为 `transformer_group_a_id`。
- 该字段不是“变压器组”实体引用，不创建变压器组节点。
- 当前样例值可匹配 `transformer.inst_id`，但只用于校验 `rpp → ups_group → transformer` 解析结果。
- 该字段不参与供电路径遍历，也不生成 `rpp → transformer` 直连关系。

#### 来源时间

- `source_created_at` 使用非空 `create_time`。
- `source_updated_at` 使用非空 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

### 3.4 当前不保存字段

以下字段不进入 RPP 节点：

- `obj_id`
- `building`
- `idc_room`
- `module`
- `city`
- `row_in_num`
- `row_power_type`
- `row_rated_current`
- `row_rated_voltage`
- `creator`
- `modifier`
- `supplier_account`

这些字段后续只有在出现明确空间或电气查询需求时，才通过显式模型变更加入。

## 4. 关系解析

### 4.1 机柜 A/B 路连接 RPP

机柜分别使用：

```text
cabinet.row_switch_id_A → rpp.inst_id → rpp.uuid
cabinet.row_switch_id_B → rpp.inst_id → rpp.uuid
```

解析成功后生成：

```text
cabinet(uuid) -[power_upstream {power_path: "A"}]-> rpp(uuid)
cabinet(uuid) -[power_upstream {power_path: "B"}]-> rpp(uuid)
```

规则：

1. A、B 路独立解析，任一路失败不影响另一条已成功关系。
2. `power_path` 必须保存为规范值 `A` 或 `B`。
3. 若 A、B 路引用同一 RPP，仍保留两条关系，关系身份必须包含 `power_path`。
4. 不创建独立空开实体。
5. 该关系表示 CMDB 配置的上游引用，不证明实时通电、电流方向或切换状态。

### 4.2 RPP 连接 UPS 组

使用：

```text
rpp.ups_group_id → ups_group.inst_id → ups_group.uuid
```

解析成功后生成：

```text
rpp(uuid) -[power_upstream]-> ups_group(uuid)
```

规则：

1. `ups_group_id` 必须唯一解析到一个 UPS 组 UUID。
2. 不使用 UPS 组编码、标签或名称猜测目标。
3. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位节点或关系，并记录同步诊断。
4. `ups_group_id` 变化时生成新关系；完整成功后清理旧的 UPS 组上联关系。

### 4.3 变压器一致性校验

RPP 的候选变压器引用通过以下实际主链进行校验：

```text
rpp.ups_group_id
  → ups_group.inst_id
  → ups_group.transformer_id_up
  → transformer.inst_id
```

将主链解析出的变压器数字 ID 与 `rpp.transformer_group_a_id` 比较：

- 两者相等：记录校验通过。
- 两者不相等：记录 `conflict`，但不静默选择任一目标。
- UPS 组、变压器或引用未采集：记录 `unresolved`。
- `transformer_group_a_id` 为空：跳过该项校验，不影响 `rpp → ups_group` 关系。

无论校验结果如何，都不生成：

```text
rpp → transformer
```

### 4.4 当前不建立的数据中心关系

虽然节点保留 `idc_id`，当前不生成：

```text
rpp -[located_in]-> data_center
```

当前供电路径从机柜经 RPP、UPS 组向上遍历。RPP 的数据中心引用仅作为后续版本建模入口，不参与当前关系发布和清理。

## 5. 拓扑关系

| 关系 | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|
| `power_upstream` | `cabinet(uuid) → rpp(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `power_path` | `cabinet.row_switch_id_A/B` |
| `power_upstream` | `rpp(uuid) → ups_group(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `rpp.ups_group_id` |

关系身份规则：

- 上述来源均没有独立关系 UUID。
- `cabinet → rpp` 的 `relation_id` 由关系类型、两端完整逻辑身份和 `power_path` 确定性生成。
- `rpp → ups_group` 的 `relation_id` 由关系类型和两端完整逻辑身份确定性生成。
- 两端完整逻辑身份必须包含 `scope_id`、`source_id`、对象类型和稳定 UUID。

## 6. 同步与清理约定

1. RPP 节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 `rpp` 和 `ups_group`，建立本轮临时 `inst_id → UUID` 映射，再解析上游关系；机柜关系在机柜数据可用后解析。
4. `idc_id` 只保存为节点属性，当前不加入关系解析阶段。
5. `ups_group_id` 解析失败时记录 RPP UUID、引用值和错误类型，不生成占位 UPS 组。
6. `transformer_group_a_id` 的校验结果进入同步诊断，不改变主供电关系。
7. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
8. 本轮见到的 RPP 节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
9. 任一必要分页、身份校验、关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
10. 当前机柜或 RPP 上游引用仍存在但无法解析时，不得据此删除可能对应的旧关系；无法安全判断范围时跳过相应范围清理。
11. 完整成功后先清理过期 `cabinet → rpp` 和 `rpp → ups_group` 关系，再删除无其他有效引用的过期 RPP 节点。
12. 按单个 `inst_id`、`ups_group`、`idc` 或 `module` 查询只能清理其明确声明且完整覆盖的范围；普通排障查询不得执行消失清理。
13. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的 RPP 快照或关系候选。

## 7. 样例闭环

当前 RPP 样例：

```text
uuid                    = 9f283fc3-d029-4a4d-b312-d23b5c2c4cd5
inst_id                 = 6204
code                    = F2_IT06AC-8A
idc_id                  = 451
ups_group_id            = 1156
transformer_group_a_id  = 1588
```

已验证：

1. 机柜样例 `row_switch_id_A=6204` 可匹配该 RPP 的 `inst_id=6204`，生成 A 路 `cabinet → rpp`。
2. `ups_group_id=1156` 可匹配 UPS 组样例 `inst_id=1156`，目标 UUID 为 `1461bad1-8c8b-47c8-ad37-dc12399bdeb4`。
3. UPS 组样例的 `transformer_id_up=1588` 与 RPP 的 `transformer_group_a_id=1588` 一致。
4. `idc_id=451` 可作为后续版本连接 `data_center.inst_id=451` 的来源证据，但当前不解析、不建边。
5. B 路机柜引用 `row_switch_id_B=6114` 当前未提供目标 RPP 样例；同步时按同一规则解析，未解析前不生成 B 路关系。

当前主链为：

```text
cabinet
  -[power_upstream {power_path: "A"}]->
rpp(9f283fc3-d029-4a4d-b312-d23b5c2c4cd5)
  -[power_upstream]->
ups_group(1461bad1-8c8b-47c8-ad37-dc12399bdeb4)
```

## 8. 来源证据

- [space_rpp.md](../../cmdb/space_rpp.md)
- [space_ups_group.md](../../cmdb/space_ups_group.md)
- [space_cainet.md](../../cmdb/space_cainet.md)
- [cabinet.md](cabinet.md)
- [data_center.md](data_center.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
