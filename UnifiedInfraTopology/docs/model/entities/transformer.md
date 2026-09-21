# 变压器实体定义

状态：核心规格拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于表达 UPS 组的供电上游边界和变压器备用关系。

## 1. 设计目标

`transformer` 表示 CMDB `idc_transformer` 对象。本阶段采用“拓扑 + 核心规格”模型，保存身份、编码、数据中心预留引用、备用变压器引用、品牌、型号、额定容量和同步时间。

本实体遵守以下边界：

1. 变压器节点以 `idc_transformer.uuid` 作为最终身份。
2. 保留 `inst_id`，用于解析 `ups_group.transformer_id_up`、其他实体的校验引用、备用变压器引用和按数字 ID 查询 CMDB；数字 ID 不替代 UUID。
3. UPS 组通过 `transformer_id_up` 建立 `ups_group → transformer` 的 `power_upstream` 关系。
4. 保留 `standby_transformer`，目标唯一解析时建立 `transformer → transformer` 的 `has_standby` 关系。
5. `has_standby` 仅表达 CMDB 配置的备用引用，不属于当前供电路径，不参与默认故障影响遍历。
6. 保留来源 `idc`，作为后续版本建立 `transformer → data_center` 关系的入口；当前版本不解析、不建边，也不记录该引用的关系解析诊断。
7. 保留品牌、型号和额定容量作为核心设备规格；容量单位未由来源文档定义，按原始数值保存，不自行换算。
8. 不保存楼栋、模组、标签、绝缘等级、温控阈值、低压侧配置、业务属性和生命周期日期。
9. 不展开馈线、电力线路、发电机组或市电实体及关系；变压器是本阶段主供电链的上游边界。
10. NebulaGraph 保存变压器节点和已解析成功的关系；MySQL 不保存变压器快照或未解析关系候选。

## 2. 变压器查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `idc_transformer`：

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

只有全部分页成功后，本轮变压器节点集合才可视为完整。

### 2.2 按数字 ID 查询

接口支持按 `inst_id` 精确查询：

```json
{
  "condition": {
    "inst_id": 1588
  }
}
```

该查询用于解析 UPS 组上游、备用变压器引用、关系排障或单对象核对。单对象查询不构成完整同步范围，不能据此清理其他变压器节点或关系。

### 2.3 按数据中心查询

接口支持按 `idc` 查询。当前模型保留 `idc`，但不建立变压器到数据中心的关系。该条件只可用于来源排障或明确的受限采集范围，不能改变当前图关系语义。

## 3. 变压器图节点

### 3.1 身份

变压器最终身份使用：

```text
transformer.uuid
```

逻辑变压器身份：

```text
source_id:transformer:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `idc`
- `standby_transformer`
- `transformer_brand`
- `transformer_type`
- `transformer_id_tag`

编码、备用引用、数据中心引用或设备规格变化时，只更新节点属性及相应关系，不创建新变压器节点。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `idc_transformer.uuid` | 变压器稳定身份 |
| `inst_id` | `idc_transformer.inst_id` | CMDB 数字 ID，用于查询、关系解析和来源追溯 |
| `code` | `idc_transformer.code` | 变压器编码和主要展示名称 |
| `idc_id` | `idc_transformer.idc` | 数据中心数字引用；仅为后续版本关系预留，当前不解析 |
| `standby_transformer_id` | `idc_transformer.standby_transformer` | 备用变压器数字引用，用于解析目标 `transformer.inst_id` |
| `brand` | `idc_transformer.transformer_brand` | 变压器品牌原始值 |
| `model` | `idc_transformer.transformer_type` | 变压器型号原始值 |
| `rated_capacity` | `idc_transformer.transformer_capacity` | 来源额定容量原始数值；单位未知，不自行换算 |
| `source_created_at` | `idc_transformer.create_time` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `idc_transformer.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值和数字引用

- 字符串去除首尾空白后保存，空字符串统一写为 `NULL`。
- `inst_id`、`idc_id` 和 `standby_transformer_id` 按来源整数保存。
- 数字引用为空、为 `0` 或非法值时统一写为 `NULL`。
- UUID 为空时不能建立变压器节点，并记录同步诊断。
- 数字 ID 变化不改变变压器身份。

#### 编码和展示名称

节点只保存来源 `code`。展示名称按以下顺序选择，不额外保存派生字段：

```text
code 非空 → code
否则 → uuid
```

`code` 变化不影响变压器身份。

#### 数据中心预留引用

- 来源 `idc` 规范保存为节点属性 `idc_id`。
- 潜在映射为：

```text
transformer.idc_id → data_center.inst_id
```

- 当前版本不执行该解析、不建立 `transformer → data_center` 关系。
- 当前版本不因 `idc_id` 为空、目标缺失、匹配不唯一或与 UPS 组不一致而记录关系诊断。
- 后续版本若启用该关系，必须经过独立模型评审，并使用数据中心 UUID 作为最终端点。

#### 备用变压器引用

- 来源 `standby_transformer` 规范保存为 `standby_transformer_id`。
- `standby_transformer_id` 通过同一 `source_id` 下的 `transformer.inst_id` 解析目标 UUID。
- 引用为空时不建立备用关系，也不记录错误。
- 目标不存在或匹配不唯一时不创建占位节点或关系，并记录 `unresolved`。
- 当前变压器引用自身时记录 `self_reference`，不发布关系。
- 多条备用引用形成有向环时记录 `cycle`，不发布该环中的备用关系。
- 引用变化不改变变压器身份；完整成功后更新备用关系。

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

以下字段不进入变压器节点：

- `obj_id`
- `building`
- `module`
- `city`
- `transformer_id_tag`
- `transformer_insulation_level`
- `transformer_only_IT`
- `transformer_only_jd`
- `transformer_over_temperature_alarm`
- `transformer_over_temperature_cut`
- `transformer_product_time`
- `transformer_expire_time`
- `LVP_busbar`
- `LVP_generator_incoming_id`
- `LVP_generator_num`
- `LVP_logic_state`
- `generator_group`
- `power_in_id`
- `up_Feeder_id`
- `up_power_line_id`
- `creator`
- `modifier`
- `supplier_account`

其中：

- 标签、绝缘等级、业务专用和温控字段不用于当前拓扑关系。
- 低压侧、馈线、电力线路和发电机引用不展开为实体或关系。
- 产品和到期日期不进入当前拓扑模型。

## 4. 关系解析

### 4.1 UPS 组连接变压器

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
2. 不使用变压器编码、标签、品牌、型号或位置字段猜测目标。
3. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位节点或关系，并记录同步诊断。
4. UPS 组引用变化时生成新关系；完整成功后清理旧的上游变压器关系。
5. 该关系表示 CMDB 配置的主供电上游引用，不证明实时供电状态、电流方向或负载分配。

### 4.2 变压器备用关系

使用：

```text
transformer.standby_transformer_id
  → transformer.inst_id
  → transformer.uuid
```

解析成功并通过自引用、循环检查后生成：

```text
transformer(primary_uuid) -[power_relation {relation_kind: "has_standby"}]-> transformer(standby_uuid)
```

规则：

1. 关系方向与来源字段一致：持有 `standby_transformer_id` 的当前变压器指向其备用变压器。
2. `has_standby` 表示配置的备用对象，不表示当前正在由备用变压器供电。
3. 该关系不属于默认 `power_upstream` 路径，不参与默认故障影响范围查询。
4. 同一主变压器与同一备用变压器只生成一条关系。
5. 不使用编码、标签、位置或规格猜测备用目标。
6. 自引用、目标不唯一或有向环不发布关系，并记录明确诊断。
7. 若业务查询需要评估切换可能性，必须显式包含 `has_standby`，且结果只能表示候选备用配置，不能表示可切换或可用。

### 4.3 当前不建立的数据中心关系

虽然节点保留 `idc_id`，当前不生成：

```text
transformer -[spatial_relation {relation_kind: "located_in"}]-> data_center
```

该引用仅作为后续版本建模入口，不参与当前关系发布和清理。

### 4.4 当前不展开的上游关系

以下字段当前不生成关系：

```text
power_in_id
up_Feeder_id
up_power_line_id
generator_group
LVP_generator_incoming_id
```

变压器是当前主供电拓扑的上游边界，不继续展开馈线、电力线路、发电机组和市电来源。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `power_relation` | `power_upstream` | `ups_group(uuid) → transformer(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `ups_group.transformer_id_up` |
| `power_relation` | `has_standby` | `transformer(primary_uuid) → transformer(standby_uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `transformer.standby_transformer_id` |

关系身份规则：

- 上述来源均没有独立关系 UUID。
- 每条 `relation_id` 由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。
- 两端完整逻辑身份必须包含 `source_id`、对象类型和稳定 UUID。
- 不使用数字引用、编码或标签直接作为最终关系身份。
- `has_standby(A,B)` 与 `has_standby(B,A)` 是不同关系，但若共同形成循环则均不发布。

## 6. 同步与清理约定

1. 变压器节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集全部 `transformer`，建立本轮临时 `inst_id → UUID` 映射，再解析备用关系和 UPS 组上游关系。
4. `idc_id` 只保存为节点属性，当前不加入关系解析阶段。
5. 备用关系必须在完整变压器集合上执行目标解析、自引用和有向环检查。
6. `standby_transformer_id` 解析失败时记录变压器 UUID、引用值和错误类型，不生成占位变压器。
7. UPS 组上游解析失败时记录 UPS 组 UUID、引用值和错误类型，不生成占位变压器。
8. RPP 和 UPS 设备的变压器引用校验结果进入同步诊断，不创建跨层关系。
9. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
10. 本轮见到的变压器节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
11. 任一必要分页、身份校验、关系解析、循环检查或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
12. 当前 UPS 组或备用引用仍存在但无法解析时，不得据此删除可能对应的旧关系；无法安全判断范围时跳过相应范围清理。
13. 完整成功后先清理过期 `ups_group → transformer` 和 `has_standby` 关系，再删除无其他有效引用的过期变压器节点。
14. 按单个 `inst_id` 或 `idc` 查询只能清理其明确声明且完整覆盖的范围；普通排障查询不得执行消失清理。
15. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的变压器快照或关系候选。

## 7. 样例闭环

当前变压器样例：

```text
uuid                    = 8ff80fda-20d5-4193-b16e-8808d87108a5
inst_id                 = 1588
code                    = 1T203
idc_id                  = 451
standby_transformer_id  = 1593
brand                   = 许继
model                   = SCB11-2500KVA/10
rated_capacity          = 2500
```

已验证：

1. UPS 组样例 `transformer_id_up=1588` 可匹配该变压器的 `inst_id=1588`，生成 `ups_group → transformer`。
2. RPP 样例的 `transformer_group_a_id=1588` 与 UPS 组上游一致，但不生成 RPP 到变压器的直连关系。
3. 三台 UPS 样例的 `transformer_id_up=1588` 与所属 UPS 组上游一致，但不生成 UPS 到变压器的直连关系。
4. `standby_transformer_id=1593` 的目标记录当前未提供，因此本轮不生成 `has_standby`，并记录 `unresolved`。
5. `idc_id=451` 可作为后续版本连接 `data_center.inst_id=451` 的来源证据，当前不解析、不建边。
6. `rated_capacity=2500` 按来源原始数值保存；当前文档不声明其单位，也不用于推断实际负载或冗余能力。
7. `up_Feeder_id=1G207` 和 `up_power_line_id=708` 不进入节点，不继续扩展上游供电实体。

当前已闭合主链为：

```text
ups_group(1461bad1-8c8b-47c8-ad37-dc12399bdeb4)
  -[power_relation {relation_kind: "power_upstream"}]->
transformer(8ff80fda-20d5-4193-b16e-8808d87108a5)
```

当前未闭合备用引用为：

```text
transformer(inst_id=1588)
  -[has_standby，待解析]->
transformer(inst_id=1593)
```

## 8. 来源证据

- [space_transformer.md](../../cmdb/space_transformer.md)
- [space_ups_group.md](../../cmdb/space_ups_group.md)
- [space_rpp.md](../../cmdb/space_rpp.md)
- [space_ups.md](../../cmdb/space_ups.md)
- [ups_group.md](ups_group.md)
- [rpp.md](rpp.md)
- [ups.md](ups.md)
- [data_center.md](data_center.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
