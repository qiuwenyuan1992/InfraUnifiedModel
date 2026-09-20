# UPS 设备实体定义

状态：核心规格拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于表达 UPS 设备、UPS 组成员关系及设备级上游引用校验。

## 1. 设计目标

`ups` 表示 CMDB `idc_ups` 对象。它是 UPS 组中的设备成员，不直接承担组级上游变压器关系。

本阶段采用“拓扑 + 核心规格”模型，保存身份、编码、数据中心预留引用、UPS 组引用、设备级变压器校验引用、品牌、型号、额定容量和同步时间。

本实体遵守以下边界：

1. UPS 节点以 `idc_ups.uuid` 作为最终身份。
2. 保留 `inst_id`，用于按数字 ID 查询 CMDB 和来源追溯；数字 ID 不替代 UUID。
3. 保留 `ups_group`，用于建立 `ups → ups_group` 成员关系。
4. 保留 `transformer_id_up`，但只与所属 UPS 组的上游变压器引用进行一致性校验，不建立 `ups → transformer` 直连关系。
5. 保留来源 `idc`，作为后续版本建立 `ups → data_center` 关系的入口；当前版本不解析、不建边，也不记录该引用的关系解析诊断。
6. 保留 `ups_brand`、`ups_type` 和 `ups_capacity`，作为核心设备规格；容量单位未由来源文档定义，按原始数值保存，不自行换算。
7. 不保存楼栋、模组、组标签、生命周期日期、维护日期和电池明细。
8. 不根据容量、成员数量或设备规格推断并联容量、N+1、冗余等级、负载分配或故障影响。
9. NebulaGraph 保存 UPS 节点和已解析成功的成员关系；MySQL 不保存 UPS 快照或未解析关系候选。

## 2. UPS 查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `idc_ups`：

```json
{
  "fields": [],
  "page": {
    "start": 0,
    "limit": 1000,
    "sort": "inst_id"
  },
  "condition": {}
}
```

只有全部分页成功后，本轮 UPS 节点集合才可视为完整。

### 2.2 按数字 ID 查询

接口可按 `inst_id` 精确查询单台 UPS：

```json
{
  "condition": {
    "inst_id": 3255
  }
}
```

该查询用于对象核对和关系排障。单对象查询不构成完整同步范围，不能据此清理其他 UPS 节点或关系。

### 2.3 按 UPS 组查询

接口支持按 `ups_group` 查询组内设备：

```json
{
  "condition": {
    "ups_group": 1156
  }
}
```

该查询可用于成员关系核对或明确的受限范围同步。除非任务声明并完整覆盖该 UPS 组范围，否则不能作为成员消失清理依据。

### 2.4 其他查询条件

接口还支持按 `idc`、`module` 和 `transformer_id_up` 查询。当前模型保留 `idc` 和 `transformer_id_up`，但前者不建关系，后者只作校验；`module` 不进入节点。这些条件只用于来源排障或明确的受限采集范围，不能改变当前图关系语义。

## 3. UPS 图节点

### 3.1 身份

UPS 最终身份使用：

```text
ups.uuid
```

逻辑 UPS 身份：

```text
scope_id:source_id:ups:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `idc`
- `ups_group`
- `transformer_id_up`
- `ups_brand`
- `ups_type`

编码、组归属、来源引用或规格变化时，只更新节点属性及相应关系，不创建新 UPS 节点。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `scope_id` | 同步上下文 | 隔离租户或拓扑范围 |
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `idc_ups.uuid` | UPS 稳定身份 |
| `inst_id` | `idc_ups.inst_id` | CMDB 数字 ID，用于查询和来源追溯 |
| `code` | `idc_ups.code` | UPS 编码和主要展示名称 |
| `idc_id` | `idc_ups.idc` | 数据中心数字引用；仅为后续版本关系预留，当前不解析 |
| `ups_group_id` | `idc_ups.ups_group` | UPS 组数字引用，用于解析 `ups_group.inst_id` |
| `transformer_id_up` | `idc_ups.transformer_id_up` | 设备级变压器数字引用，仅用于与组上游校验 |
| `brand` | `idc_ups.ups_brand` | UPS 品牌原始值 |
| `model` | `idc_ups.ups_type` | UPS 型号原始值 |
| `rated_capacity` | `idc_ups.ups_capacity` | 来源额定容量原始数值；单位未知，不自行换算 |
| `source_created_at` | `idc_ups.create_time` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `idc_ups.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值和数字引用

- 字符串去除首尾空白后保存，空字符串统一写为 `NULL`。
- `inst_id`、`idc_id`、`ups_group_id` 和 `transformer_id_up` 按来源整数保存。
- 数字引用为空、为 `0` 或非法值时统一写为 `NULL`。
- UUID 为空时不能建立 UPS 节点，并记录同步诊断。
- 数字 ID 变化不改变 UPS 身份。

#### 编码和展示名称

节点只保存来源 `code`。展示名称按以下顺序选择，不额外保存派生字段：

```text
code 非空 → code
否则 → uuid
```

`code` 变化不影响 UPS 身份。

#### 数据中心预留引用

- 来源 `idc` 规范保存为节点属性 `idc_id`。
- 潜在映射为：

```text
ups.idc_id → data_center.inst_id
```

- 当前版本不执行该解析、不建立 `ups → data_center` 关系。
- 当前版本不因 `idc_id` 为空、目标缺失、匹配不唯一或与 UPS 组不一致而记录关系诊断。
- 后续版本若启用该关系，必须经过独立模型评审，并使用数据中心 UUID 作为最终端点。

#### UPS 组引用

- 来源 `ups_group` 规范保存为 `ups_group_id`。
- `ups_group_id` 通过 `ups_group.inst_id` 解析目标 UUID。
- 无法唯一解析时不生成成员关系，并记录同步诊断。
- 组引用变化不改变 UPS 身份；完整成功后更新成员关系。

#### 设备级变压器引用

- 来源 `transformer_id_up` 保存为同名节点属性。
- 该字段不直接解析为图关系端点，不生成 `ups → transformer`。
- 它只与所属 UPS 组的 `transformer_id_up` 比较，用于发现设备与组配置冲突。
- UPS 未解析到组时，该项校验为 `unresolved`，不能据此建立变压器关系。

#### 核心规格

- `brand`、`model` 去除首尾空白后保存；空字符串写为 `NULL`。
- `rated_capacity` 按来源数值保存，不附加或猜测单位。
- 不使用品牌、型号或容量参与身份匹配和关系解析。
- 规格变化只更新节点属性，不创建新节点。

#### 来源时间

- `source_created_at` 使用非空 `create_time`。
- `source_updated_at` 使用非空 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

### 3.4 当前不保存字段

以下字段不进入 UPS 节点：

- `obj_id`
- `building`
- `module`
- `city`
- `ups_group_id_tag`
- `ups_product_date`
- `ups_exprie_date`
- `ups_capacitor_change_date`
- `battery_brand`
- `battery_type`
- `battery_group_num`
- `battery_num`
- `battery_voltage`
- `battery_resistance`
- `battery_product_time`
- `creator`
- `modifier`
- `supplier_account`

其中：

- `ups_group_id_tag` 不作为组关系键；组归属只使用 `ups_group_id`。
- 产品、到期和电容更换日期不进入当前拓扑模型。
- 电池字段属于设备维护和部件明细，不用于当前拓扑关系。

## 4. 关系解析

### 4.1 UPS 属于 UPS 组

使用：

```text
ups.ups_group_id → ups_group.inst_id → ups_group.uuid
```

解析成功后生成：

```text
ups(uuid) -[power_relation {relation_kind: "member_of"}]-> ups_group(uuid)
```

规则：

1. `ups_group_id` 必须唯一解析到一个 UPS 组 UUID。
2. 不使用 `ups_group_id_tag`、编码、品牌、型号或位置字段猜测目标。
3. 同一 UPS 与同一 UPS 组只生成一条成员关系。
4. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位组或关系，并记录同步诊断。
5. UPS 组不保存反向成员数组；成员通过图关系查询。
6. `member_of` 表示设备编组，不表示 UPS 设备之间串联、并联方式或负载分配。

### 4.2 变压器引用一致性校验

UPS 设备的 `transformer_id_up` 与所属 UPS 组的上游引用比较：

```text
ups.transformer_id_up
  ↔ ups.ups_group_id
     → ups_group.inst_id
     → ups_group.transformer_id_up
```

校验规则：

- 两者相等：记录校验通过。
- 两者不相等：记录 `conflict`，但不改变 `member_of` 关系，也不静默选择任一变压器。
- UPS 组或引用未采集、组关系无法解析：记录 `unresolved`。
- UPS 自身 `transformer_id_up` 为空：跳过该项校验，不影响成员关系。

无论校验结果如何，都不生成：

```text
ups → transformer
```

### 4.3 当前不建立的数据中心关系

虽然节点保留 `idc_id`，当前不生成：

```text
ups -[spatial_relation {relation_kind: "located_in"}]-> data_center
```

该引用仅作为后续版本建模入口，不参与当前关系发布和清理。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `power_relation` | `member_of` | `ups(uuid) → ups_group(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `ups.ups_group_id` |

关系身份规则：

- 来源没有独立关系 UUID。
- `relation_id` 由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。
- 两端完整逻辑身份必须包含 `scope_id`、`source_id`、对象类型和稳定 UUID。
- 不使用 `ups_group_id_tag` 或数字引用直接作为最终关系身份。

## 6. 同步与清理约定

1. UPS 节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 `ups` 和 `ups_group`，建立本轮临时 `inst_id → UUID` 映射，再解析成员关系。
4. `idc_id` 只保存为节点属性，当前不加入关系解析阶段。
5. `transformer_id_up` 只加入一致性校验，不加入关系发布阶段。
6. `ups_group_id` 解析失败时记录 UPS UUID、引用值和错误类型，不生成占位 UPS 组。
7. 变压器引用校验结果进入同步诊断，不改变成员关系。
8. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
9. 本轮见到的 UPS 节点和成功解析的成员关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
10. 任一必要分页、身份校验、成员关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
11. 当前 UPS 组引用仍存在但无法解析时，不得据此删除可能对应的旧成员关系；无法安全判断范围时跳过该 UPS 范围清理。
12. 完整成功后先清理过期 `ups → ups_group` 关系，再删除无其他有效引用的过期 UPS 节点。
13. 按单个 `inst_id`、`ups_group`、`idc`、`module` 或 `transformer_id_up` 查询只能清理其明确声明且完整覆盖的范围；普通排障查询不得执行消失清理。
14. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的 UPS 快照或关系候选。

## 7. 样例闭环

当前 UPS 样例共三台：

| `inst_id` | `uuid` | `code` | `ups_group_id` | `transformer_id_up` | `brand` | `model` | `rated_capacity` |
|---:|---|---|---:|---:|---|---|---:|
| 3255 | `ddc7cfc5-1b84-43e9-8ba7-78685d38f4b1` | `F2-UPS-A12` | 1156 | 1588 | 维谛 | `EXL S1` | 500 |
| 3280 | `be8db0e1-6624-4bbd-9113-b1b894385f65` | `F2-UPS-A11` | 1156 | 1588 | 维谛 | `EXL S1` | 500 |
| 3370 | `72d59d8d-74bc-4230-b236-27e1e6527f8f` | `F2-UPS-A13` | 1156 | 1588 | 维谛 | `EXL S1` | 500 |

已验证：

1. 三台 UPS 的 `ups_group_id=1156` 均可匹配 UPS 组 UUID `1461bad1-8c8b-47c8-ad37-dc12399bdeb4`。
2. 每台 UPS 分别建立一条 `member_of` 关系，不根据来源数量字段创建成员。
3. 三台 UPS 的 `transformer_id_up=1588` 均与 UPS 组的 `transformer_id_up=1588` 一致。
4. 不生成任何 `ups → transformer` 直连关系。
5. 三台 UPS 的 `idc_id=451` 可作为后续版本连接 `data_center.inst_id=451` 的来源证据，当前不解析、不建边。
6. `rated_capacity=500` 按来源原始数值保存；当前文档不声明其单位，也不汇总为 UPS 组容量。
7. 电池字段和产品、到期、电容更换日期均不进入节点。

当前成员关系为：

```text
ups(ddc7cfc5-1b84-43e9-8ba7-78685d38f4b1) ─┐
ups(be8db0e1-6624-4bbd-9113-b1b894385f65) ─┼─[member_of]→ ups_group(1461bad1-8c8b-47c8-ad37-dc12399bdeb4)
ups(72d59d8d-74bc-4230-b236-27e1e6527f8f) ─┘
```

## 8. 来源证据

- [space_ups.md](../../cmdb/space_ups.md)
- [space_ups_group.md](../../cmdb/space_ups_group.md)
- [space_transformer.md](../../cmdb/space_transformer.md)
- [ups_group.md](ups_group.md)
- [data_center.md](data_center.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
