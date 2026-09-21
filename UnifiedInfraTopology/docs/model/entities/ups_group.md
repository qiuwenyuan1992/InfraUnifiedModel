# UPS 组实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于聚合 UPS 设备，并连接 RPP 与上游变压器。

## 1. 设计目标

`ups_group` 表示 CMDB `idc_ups_group` 对象。它是供电主链中的组级节点，不等同于 UPS 设备，也不表示串联的电气设备。

本阶段采用严格的“拓扑最小”模型，只保存身份、编码、数据中心预留引用、上游变压器引用和同步时间。

本实体遵守以下边界：

1. UPS 组节点以 `idc_ups_group.uuid` 作为最终身份。
2. 保留 `inst_id`，用于解析 `rpp.ups_group`、`ups.ups_group` 和按数字 ID 查询 CMDB；数字 ID 不替代 UUID。
3. 保留 `transformer_id_up`，用于建立 `ups_group → transformer` 的供电上游关系。
4. 保留来源 `idc`，作为后续版本建立 `ups_group → data_center` 关系的入口；当前版本不解析、不建边，也不记录该引用的关系解析诊断。
5. UPS 设备通过自身 `ups_group` 字段建立 `ups → ups_group` 成员关系；组节点不保存成员列表。
6. 不保存 `ups_group_num` 数量快照；成员数量通过实际 `member_of` 关系计算。
7. 不保存组标签、旁路配置和业务专用字段。
8. 不建立 `rpp → transformer` 或 `ups → transformer` 跨层捷径。
9. 不根据成员数量、设备容量或组字段推断 N+1、并联容量、冗余等级或故障影响。
10. NebulaGraph 保存 UPS 组节点和已解析成功的关系；MySQL 不保存 UPS 组快照或未解析关系候选。

## 2. UPS 组查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `idc_ups_group`：

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

只有全部分页成功后，本轮 UPS 组节点集合才可视为完整。

### 2.2 按数字 ID 查询

接口支持按 `inst_id` 精确查询：

```json
{
  "condition": {
    "inst_id": 1156
  }
}
```

该查询用于解析 RPP 或 UPS 设备的组引用、关系排障和单对象核对。单对象查询不构成完整同步范围，不能据此清理其他 UPS 组节点或关系。

### 2.3 当前不使用的查询条件

接口还支持按 `idc` 和 `module` 查询。当前模型保留 `idc`，但不建立 UPS 组到数据中心的关系；`module` 不进入节点。这些条件只可用于来源排障或明确的受限采集范围，不能改变当前图关系语义。

## 3. UPS 组图节点

### 3.1 身份

UPS 组最终身份使用：

```text
ups_group.uuid
```

逻辑 UPS 组身份：

```text
source_id:ups_group:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `idc`
- `transformer_id_up`

编码、数据中心引用或上游变压器变化时，只更新节点属性及相应关系，不创建新 UPS 组节点。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `idc_ups_group.uuid` | UPS 组稳定身份 |
| `inst_id` | `idc_ups_group.inst_id` | CMDB 数字 ID，用于 RPP/UPS 引用解析、查询和来源追溯 |
| `code` | `idc_ups_group.code` | UPS 组编码和主要展示名称 |
| `idc_id` | `idc_ups_group.idc` | 数据中心数字引用；仅为后续版本关系预留，当前不解析 |
| `transformer_id_up` | `idc_ups_group.transformer_id_up` | 上游变压器数字引用，用于解析 `transformer.inst_id` |
| `source_created_at` | `idc_ups_group.create_time` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `idc_ups_group.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值和数字引用

- 字符串去除首尾空白后保存，空字符串统一写为 `NULL`。
- `inst_id`、`idc_id` 和 `transformer_id_up` 按来源整数保存。
- 数字引用为空、为 `0` 或非法值时统一写为 `NULL`。
- UUID 为空时不能建立 UPS 组节点，并记录同步诊断。
- 数字 ID 变化不改变 UPS 组身份。

#### 编码和展示名称

节点只保存来源 `code`。展示名称按以下顺序选择，不额外保存派生字段：

```text
code 非空 → code
否则 → uuid
```

`code` 变化不影响 UPS 组身份。

#### 数据中心预留引用

- 来源 `idc` 规范保存为节点属性 `idc_id`。
- 潜在映射为：

```text
ups_group.idc_id → data_center.inst_id
```

- 当前版本不执行该解析、不建立 `ups_group → data_center` 关系。
- 当前版本不因 `idc_id` 为空、目标缺失、匹配不唯一或与其他对象不一致而记录关系诊断。
- 后续版本若启用该关系，必须经过独立模型评审，并使用数据中心 UUID 作为最终端点。

#### 上游变压器引用

- 来源 `transformer_id_up` 直接保存为同名节点属性。
- `transformer_id_up` 通过 `transformer.inst_id` 解析目标 UUID。
- 无法唯一解析时不生成上游关系，并记录同步诊断。
- 引用变化不改变 UPS 组身份；完整成功后更新上游关系。

#### 来源时间

- `source_created_at` 使用非空 `create_time`。
- `source_updated_at` 使用非空 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

### 3.4 当前不保存字段

以下字段不进入 UPS 组节点：

- `obj_id`
- `building`
- `module`
- `city`
- `ups_group_num`
- `ups_group_id_tag`
- `ups_external_bypass`
- `ups_only_IT`
- `ups_only_jd`
- `ups_proportion_jd`
- `creator`
- `modifier`
- `supplier_account`

其中：

- `ups_group_num` 不作为成员数量事实；实际成员数量通过入向 `member_of` 关系计算。
- `ups_external_bypass` 不等于实时旁路运行状态，不进入当前拓扑模型。
- 业务专用字段不用于供电关系解析。

## 4. 关系解析

### 4.1 RPP 连接 UPS 组

使用：

```text
rpp.ups_group_id → ups_group.inst_id → ups_group.uuid
```

解析成功后生成：

```text
rpp(uuid) -[power_relation {relation_kind: "power_upstream"}]-> ups_group(uuid)
```

规则：

1. `rpp.ups_group_id` 必须唯一解析到一个 UPS 组 UUID。
2. 不使用 UPS 组编码或标签猜测目标。
3. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位节点或关系，并记录同步诊断。
4. RPP 引用变化时生成新关系；完整成功后清理旧的 UPS 组上联关系。

### 4.2 UPS 设备属于 UPS 组

使用：

```text
ups.ups_group_id → ups_group.inst_id → ups_group.uuid
```

解析成功后生成：

```text
ups(uuid) -[power_relation {relation_kind: "member_of"}]-> ups_group(uuid)
```

规则：

1. 成员关系只以 UPS 设备来源字段 `ups_group` 为事实来源。
2. 不使用 `ups_group_id_tag`、名称、编码或成员数量猜测归属。
3. 同一 UPS 与同一 UPS 组只生成一条成员关系。
4. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位组或关系，并记录同步诊断。
5. UPS 组不保存反向成员数组；成员通过图关系查询。
6. `member_of` 表示组归属，不表示 UPS 设备之间串联，也不表示负载分配方式。

### 4.3 UPS 组连接变压器

使用：

```text
ups_group.transformer_id_up → transformer.inst_id → transformer.uuid
```

解析成功后生成：

```text
ups_group(uuid) -[power_relation {relation_kind: "power_upstream"}]-> transformer(uuid)
```

规则：

1. `transformer_id_up` 必须唯一解析到一个变压器 UUID。
2. 不使用变压器编码、标签或位置字段猜测目标。
3. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位节点或关系，并记录同步诊断。
4. 引用变化时生成新关系；完整成功后清理旧的上游变压器关系。
5. 该关系表示 CMDB 配置的上游引用，不证明实时供电状态、电流方向或切换状态。

### 4.4 UPS 设备变压器引用校验

UPS 设备来源中的 `transformer_id_up` 不生成 `ups → transformer` 关系，只与所属 UPS 组的上游变压器引用比较：

```text
ups.transformer_id_up
  ↔ ups.ups_group_id
     → ups_group.inst_id
     → ups_group.transformer_id_up
```

校验规则：

- 两者相等：记录校验通过。
- 两者不相等：记录 `conflict`，不静默选择任一目标。
- UPS 组、变压器或引用未采集：记录 `unresolved`。
- UPS 自身引用为空：跳过该项校验，不影响 `ups → ups_group` 成员关系。

无论校验结果如何，都不生成：

```text
ups → transformer
```

### 4.5 RPP 变压器引用校验

RPP 的 `transformer_group_a_id` 与 UPS 组的 `transformer_id_up` 进行一致性校验：

```text
rpp.transformer_group_a_id
  ↔ rpp.ups_group_id
     → ups_group.inst_id
     → ups_group.transformer_id_up
```

校验结果进入同步诊断，不生成 `rpp → transformer` 关系，也不替代 UPS 组的上游关系。

### 4.6 当前不建立的数据中心关系

虽然节点保留 `idc_id`，当前不生成：

```text
ups_group -[spatial_relation {relation_kind: "located_in"}]-> data_center
```

该引用仅作为后续版本建模入口，不参与当前关系发布和清理。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `power_relation` | `power_upstream` | `rpp(uuid) → ups_group(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `rpp.ups_group_id` |
| `power_relation` | `member_of` | `ups(uuid) → ups_group(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `ups.ups_group_id` |
| `power_relation` | `power_upstream` | `ups_group(uuid) → transformer(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `ups_group.transformer_id_up` |

关系身份规则：

- 上述来源均没有独立关系 UUID。
- 每条 `relation_id` 由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。
- 两端完整逻辑身份必须包含 `source_id`、对象类型和稳定 UUID。
- 不使用组标签、设备标签或数字引用直接作为最终关系身份。

## 6. 同步与清理约定

1. UPS 组节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 `ups_group` 和 `transformer`，建立本轮临时 `inst_id → UUID` 映射，再解析组上游关系；RPP 和 UPS 关系在对应数据可用后解析。
4. `idc_id` 只保存为节点属性，当前不加入关系解析阶段。
5. `transformer_id_up` 解析失败时记录 UPS 组 UUID、引用值和错误类型，不生成占位变压器。
6. UPS 设备和 RPP 的变压器引用校验结果进入同步诊断，不改变主供电关系。
7. `ups_group_num` 不进入模型，也不参与成员完整性判断；来源返回三台 UPS 只证明当前样例有三条成员引用。
8. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
9. 本轮见到的 UPS 组节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
10. 任一必要分页、身份校验、关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
11. 当前 RPP、UPS 或变压器引用仍存在但无法解析时，不得据此删除可能对应的旧关系；无法安全判断范围时跳过相应范围清理。
12. 完整成功后先清理过期 `rpp → ups_group`、`ups → ups_group` 和 `ups_group → transformer` 关系，再删除无其他有效引用的过期 UPS 组节点。
13. 按单个 `inst_id`、`idc` 或 `module` 查询只能清理其明确声明且完整覆盖的范围；普通排障查询不得执行消失清理。
14. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的 UPS 组快照或关系候选。

## 7. 样例闭环

当前 UPS 组样例：

```text
uuid               = 1461bad1-8c8b-47c8-ad37-dc12399bdeb4
inst_id            = 1156
code               = F2-UPS-A1
idc_id             = 451
transformer_id_up  = 1588
```

已验证：

1. RPP 样例 `ups_group_id=1156` 可匹配该 UPS 组的 `inst_id=1156`。
2. 三台 UPS 设备 `inst_id=3255/3280/3370` 均使用 `ups_group=1156`，可分别建立一条 `member_of` 关系。
3. 该 UPS 组的 `transformer_id_up=1588` 可匹配变压器 UUID `8ff80fda-20d5-4193-b16e-8808d87108a5`。
4. 三台 UPS 设备自身的 `transformer_id_up` 均为 `1588`，与组上游一致，但不生成 UPS 到变压器的直连关系。
5. RPP 的 `transformer_group_a_id=1588` 与组上游一致，但不生成 RPP 到变压器的直连关系。
6. `idc_id=451` 可作为后续版本连接 `data_center.inst_id=451` 的来源证据，当前不解析、不建边。
7. 来源 `ups_group_num=3` 与当前三条成员样例数量相同，但该字段不进入节点，也不用于推断冗余或成员完整性。

当前主链和成员关系为：

```text
rpp(9f283fc3-d029-4a4d-b312-d23b5c2c4cd5)
  -[power_relation {relation_kind: "power_upstream"}]->
ups_group(1461bad1-8c8b-47c8-ad37-dc12399bdeb4)
  -[power_relation {relation_kind: "power_upstream"}]->
transformer(8ff80fda-20d5-4193-b16e-8808d87108a5)

ups(ddc7cfc5-1b84-43e9-8ba7-78685d38f4b1) ─┐
ups(be8db0e1-6624-4bbd-9113-b1b894385f65) ─┼─[member_of]→ ups_group
ups(72d59d8d-74bc-4230-b236-27e1e6527f8f) ─┘
```

## 8. 来源证据

- [space_ups_group.md](../../cmdb/space_ups_group.md)
- [space_rpp.md](../../cmdb/space_rpp.md)
- [space_ups.md](../../cmdb/space_ups.md)
- [space_transformer.md](../../cmdb/space_transformer.md)
- [rpp.md](rpp.md)
- [data_center.md](data_center.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
