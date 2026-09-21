# 数据中心实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-18 已确认的 CMDB 规则和样例整理；用于表达机柜的顶层物理归属和数据中心同步范围。

## 1. 设计目标

`data_center` 表示 CMDB `idc` 对象。本阶段采用严格的“拓扑最小”模型，只保存身份、中文名称、短编码、展示位置和同步时间，不承载运行状态、容量统计、联系人或业务运营信息。

本实体遵守以下边界：

1. 数据中心节点以 `idc.uuid` 作为最终身份。
2. 保留 `inst_id`，用于解析 `cabinet.idc_id` 和按数字 ID 查询 CMDB；数字 ID 不替代 UUID。
3. 中文名称只使用 `cn_name`；`alias` 和 `en_name` 均丢弃。
4. 保留 `code` 作为数据中心短编码。
5. 保留 `address` 和 `location` 作为展示位置，不据此推导地域实体。
6. `city_id`、`zone_id`、`geographic_location_id` 全部丢弃。
7. `latitude`、`longitude` 不保存；当前样例均为 `0`，且来源未说明是否表示有效坐标。
8. 当前只建立 `cabinet → data_center` 的 `located_in` 关系。
9. 不建立 `device → data_center` 捷径，也不建立 `pod → data_center`、数据中心到城市或地域层级关系。
10. NebulaGraph 保存数据中心节点和已解析成功的关系；MySQL 不保存数据中心快照或未解析关系候选。

## 2. 数据中心查询

### 2.1 全量采集

完整同步按 `inst_id` 排序并分页读取 `idc`：

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

只有全部分页成功后，本轮数据中心节点集合才可视为完整。

### 2.2 按数字 ID 查询

接口支持按 `inst_id` 精确查询：

```json
{
  "condition": {
    "inst_id": 451
  }
}
```

该查询用于解析机柜的 `idc_id`、关系排障或单对象核对。单对象查询不构成完整同步范围，不能据此清理其他数据中心节点或关系。

## 3. 数据中心图节点

### 3.1 身份

数据中心最终身份使用：

```text
data_center.uuid
```

逻辑数据中心身份：

```text
source_id:data_center:uuid
```

以下字段均不能替代 UUID：

- `inst_id`
- `code`
- `cn_name`
- `address`
- `location`

数据中心改名、编码调整或地址更新时，只更新节点属性，不创建新节点。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `idc.uuid` | 数据中心稳定身份 |
| `inst_id` | `idc.inst_id` | CMDB 数字 ID，用于查询、机柜引用解析和来源追溯 |
| `code` | `idc.code` | 数据中心短编码 |
| `cn_name` | `idc.cn_name` | 唯一名称字段和主要展示名称 |
| `address` | `idc.address` | 详细地址展示值 |
| `location` | `idc.location` | 来源位置简称或位置展示值 |
| `source_created_at` | `idc.created_at` | 来源创建时间，与本项目 `created_at` 区分 |
| `source_updated_at` | `idc.updated_at` 或 `idc.last_time` | 来源更新时间，不用于全量消失判断 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

### 3.3 字段规范

#### 空值

- 字符串去除首尾空白后保存。
- 空字符串统一写为 `NULL`。
- `inst_id` 按来源整数保存；空值、`0` 或非法值不能用于关系解析。
- UUID 为空时不能建立数据中心节点，并记录同步诊断。

#### 名称和展示

数据中心只保存 `cn_name`，不保存或解析 `alias`、`en_name`。展示名称按以下顺序选择，不额外保存派生字段：

```text
cn_name 非空 → cn_name
否则 code 非空 → code
否则 → uuid
```

`cn_name` 或 `code` 变化不影响数据中心身份。

#### 地址和位置

- `address` 和 `location` 只作为来源展示属性。
- 不从地址字符串解析省、市、区、经纬度或地域节点。
- 不使用地址、位置简称或名称匹配机柜的数据中心引用。
- `address` 和 `location` 为空不影响数据中心节点建立。

#### 来源时间

- `source_created_at` 使用非空 `created_at`。
- `source_updated_at` 优先使用非空且不是默认占位值的 `updated_at`，否则使用 `last_time`。
- 空字符串和 `2000-01-01 00:00:00` 等已知默认占位时间写为 `NULL`。
- 来源时间不用于判断全量结果中对象是否消失；旧数据清理只使用本项目 `synced_at`。

### 3.4 当前不保存字段

以下字段不进入数据中心节点：

- `id`
- `obj_id`
- `action_id`
- `alias`
- `en_name`
- `city_id`
- `zone_id`
- `geographic_location_id`
- `latitude`
- `longitude`
- `logic_idc_id`
- `mounted_node_id`
- `status`
- `is_delete`
- `is_sync`
- `idc_ops_status`
- `idc_service_level`
- `open_at`
- `close_at`
- `type`
- `attribute`
- `group`
- `rental_mode`
- `manager_type`
- `monitor`
- `is_it_oms`
- `is_core_4_gpu`
- `it_service_time`
- `cabinet_num`
- `cabinet_power_num`
- `device_num`
- `server_num`
- `network_device_num`
- `external_num`
- `other_num`
- `idc_onsite_number`
- `jira_key`
- `jira_delete`
- `net_zone`
- `class_ids[]`
- `support_ids[]` 及各类 `support_*_ids[]`
- 管理部门、负责人、资产人员、外包人员、值班电话、邮箱和支持人员字段
- `modifier`、`username`、`supplier_account`

状态和统计快照后续只有在出现明确查询需求时，才通过显式模型变更加入。来源统计不能替代图中实际机柜和设备关系计数。

## 4. 关系解析

### 4.1 机柜属于数据中心

使用：

```text
cabinet.idc_id → data_center.inst_id → data_center.uuid
```

解析成功后生成：

```text
cabinet(uuid) -[spatial_relation {relation_kind: "located_in"}]-> data_center(uuid)
```

规则：

1. `cabinet.idc_id` 必须唯一解析到一个 `data_center.inst_id`。
2. 最终关系端点使用机柜 UUID 和数据中心 UUID。
3. 不使用 `cabinet.phy_idc_id`；该字段已在机柜模型中丢弃。
4. 不使用数据中心名称、编码、地址或位置简称猜测目标。
5. 引用为空、为 `0`、目标不存在或匹配不唯一时，不创建占位节点或关系，并记录同步诊断。
6. 机柜的 `idc_id` 变化时生成新关系；完整成功后清理旧的数据中心归属关系。

### 4.2 不建立的关系

当前不建立：

```text
device → data_center
pod → data_center
data_center → city
data_center → zone
data_center → geographic_location
data_center → logic_idc
```

原因：

- `device → data_center` 可通过 `device → cabinet → data_center` 遍历得到，直连会形成重复事实。
- POD 的逻辑 IDC 引用与数据中心不是已确认的同一语义。
- 当前不设计城市、区域、地理位置或逻辑 IDC 实体，且相应数字 ID 已确认丢弃。

## 5. 拓扑关系

| Edge Type | `relation_kind` | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|---|
| `spatial_relation` | `located_in` | `cabinet(uuid) → data_center(uuid)` | `relation_id`、`relation_kind`、`source_id`、`created_at`、`synced_at` | 无 | `cabinet.idc_id` |

关系身份规则：

- 来源没有独立关系 UUID。
- `relation_id` 由 `Edge Type`、`relation_kind` 和两端完整逻辑身份确定性生成。
- 两端完整逻辑身份必须包含 `source_id`、对象类型和稳定 UUID。
- 同一机柜在同一时刻只应有一条已发布的数据中心归属关系。

## 6. 同步与清理约定

1. 数据中心节点按 `uuid` 去重；同一 `source_id` 下 UUID 必须唯一。
2. 同一 `inst_id` 映射多个 UUID，或同一 UUID 同时出现多个 `inst_id` 时，本轮报身份冲突。
3. 完整同步应先完整分页采集 `data_center`，建立本轮临时 `inst_id → UUID` 映射，再解析机柜关系。
4. 临时映射仅用于本轮关系解析，不写入 MySQL；节点中的 `inst_id` 用于来源查询和追溯。
5. 机柜关系解析失败时记录机柜 UUID、`idc_id` 和错误类型，不生成占位数据中心。
6. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
7. 本轮见到的数据中心节点和成功解析的机柜关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
8. 任一必要分页、身份校验、关系解析或图写入失败时，不执行受影响范围的旧关系和旧节点清理。
9. 当前机柜引用仍存在但无法解析时，不得据此删除可能对应的旧关系；无法安全判断范围时跳过该机柜范围清理。
10. 完整成功后先清理过期 `cabinet → data_center` 关系，再删除无其他有效引用的过期数据中心节点。
11. 按单个 `inst_id` 查询只用于对象核对或引用解析，不允许据此执行全量消失清理。
12. 重试、恢复或图重建均重新读取 CMDB，不依赖 MySQL 中的数据中心快照或关系候选。

## 7. 样例闭环

当前数据中心样例：

```text
uuid       = 0e32c4d6-a29f-4d8b-94c8-38ac791e363d
inst_id    = 451
code       = RH
cn_name    = 廊坊_京东_润惠
address    = 河北省廊坊市广阳区经济技术开发区润惠道66号
location   = 润惠
```

已验证：

1. 机柜样例 `cabinet.uuid=c0b7b4f5-c5b2-ab3d-af4e-da3fbe7c055b` 的 `idc_id=451`。
2. `idc_id=451` 唯一匹配当前数据中心样例的 `inst_id=451`。
3. 最终建立：

```text
cabinet(c0b7b4f5-c5b2-ab3d-af4e-da3fbe7c055b)
  -[spatial_relation {relation_kind: "located_in"}]->
data_center(0e32c4d6-a29f-4d8b-94c8-38ac791e363d)
```

4. 来源中的 `alias`、`en_name`、地域数字 ID、坐标、状态和统计字段不进入节点。

## 8. 来源证据

- [space_idc.md](../../cmdb/space_idc.md)
- [space_cainet.md](../../cmdb/space_cainet.md)
- [cabinet.md](cabinet.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
