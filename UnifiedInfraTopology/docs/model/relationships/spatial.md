# 空间关系模型

状态：已确认采用领域统一 Edge Type。

## 1. 建模结论

空间领域统一使用一个 Edge Type：

```text
spatial_relation
```

具体空间语义由 `relation_kind` 区分：

```text
located_in
member_of
tagged_with
```

这些关系均来自 CMDB。统一 Edge Type 只改变图 Schema 的组织方式，不改变来源事实、端点或解析规则。

## 2. 端点矩阵

| `relation_kind` | 起点 | 终点 | 来源解析 | 业务属性 |
|---|---|---|---|---|
| `member_of` | `device(device_sn)` | `pod(uuid)` | 管理面解析 `pod_uuid`；计算面逐项解析 `compute_plane[].pod_id → pod.inst_id` | `plane` |
| `located_in` | `device(device_sn)` | `cabinet(uuid)` | `device.cabinet_uuid → cabinet.uuid` | 无 |
| `located_in` | `cabinet(uuid)` | `data_center(uuid)` | `cabinet.idc_id → data_center.inst_id → data_center.uuid` | 无 |
| `tagged_with` | `cabinet(uuid)` | `pod(uuid)` | 逐项解析 `cabinet.pod_ids[] → pod.inst_id → pod.uuid` | 无 |
| `located_in` | `room(uuid)` | `data_center(uuid)` | `room.idc_id → data_center.inst_id → data_center.uuid` | 无 |
| `located_in` | `cabinet(uuid)` | `room(uuid)` | `cabinet.phy_room_id → room.inst_id → room.uuid` | 无 |
| `located_in` | `rpp(inst_id)` | `room(uuid)` | `rpp.idc_room → room.inst_id → room.uuid` | 无 |

## 3. 关系表达

```text
device
  -[spatial_relation {relation_kind: "located_in"}]->
cabinet
  -[spatial_relation {relation_kind: "located_in"}]->
data_center

device
  -[spatial_relation {
      relation_kind: "member_of",
      plane: "management|compute"
    }]->
pod

cabinet
  -[spatial_relation {relation_kind: "tagged_with"}]->
pod
```

`POD` 是逻辑拓扑范围，不是物理容器。因此 `member_of`、`tagged_with` 与 `located_in` 必须保持不同的 `relation_kind`。

## 4. 解析规则

### 4.1 设备属于 POD

1. 管理面来源使用设备的 `pod_uuid`；必要时使用同条记录中已确认的管理面引用解析。
2. 计算面逐项解析 `compute_plane[].pod_id → pod.inst_id`。
3. `plane` 是关系身份的一部分，避免管理面和计算面关系互相覆盖。
4. 同一设备、POD 和 `plane` 只保存一条关系。
5. 不创建 `compute_plane` 节点。

### 4.2 设备位于机柜

1. 只使用稳定 `cabinet_uuid` 解析机柜。
2. 当前不保存设备 U 位，也不创建 U 位节点。
3. 引用为空或无法唯一解析时不建边，并记录同步诊断。
4. 设备更换机柜不改变设备身份；完整同步成功后替换关系。

### 4.3 机柜位于数据中心

1. 使用 `cabinet.idc_id → data_center.inst_id` 解析目标 UUID。
2. `phy_idc_id` 不参与关系解析。
3. 保留机柜到数据中心的直接关系，ROOM 同时根据自身 `idc_id` 建立到数据中心的关系。
4. RPP、UPS 组、UPS 和变压器不建立到数据中心的直接关系。

### 4.4 机柜关联 POD 标签

1. 逐项解析 `cabinet.pod_ids[]`。
2. `pod_id` 单值字段不进入当前关系模型。
3. 该关系表示 CMDB 标签或规划归属，不证明机柜内所有设备都属于该 POD。
4. 不从 `cabinet → pod` 推导 `device → pod`；设备 POD 关系必须使用设备自身来源证据。

### 4.5 ROOM 与房间引用

完整采集选定 IDC 的 `room_condition={idc_id, status: 1}` 和 `rpp_condition={idc}`，配置 IDC 冲突或额外窄化条件阻止采集。
ROOM 稳定身份必须为 UUID；`inst_id` 只解析 `cabinet.phy_room_id` 与 `rpp.idc_room`，两种目录身份任一冲突均禁止任意选择，记录 `ambiguous_room`；缺失或空引用记录 `missing_room` / `empty_room_reference`。
ROOM 的 `idc_id` 必须解析实际数据中心目录，不使用所选 IDC 伪造边。新增关系不替代既有关系，不推导子房间树。

## 5. 属性

所有 `spatial_relation` 保存公共属性：

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
| `plane` | `member_of` | `management` 或 `compute` |

## 6. 关系身份

无来源关系 UUID 时，确定性生成：

```text
spatial_relation + relation_kind + 两端完整逻辑身份 + 必要业务区分字段
```

`member_of` 必须将 `plane` 纳入身份；其他当前空间关系不需要额外区分字段。

## 7. 查询语义

查询整个空间领域时遍历 `spatial_relation`；查询精确业务语义时必须过滤 `relation_kind`。

```ngql
-- 查询设备的物理位置链
MATCH p=(device)-[edges:spatial_relation*1..2]->(location)
WHERE id(device) == $device_vid
  AND ALL(
    edge IN edges
    WHERE edge.spatial_relation.relation_kind == "located_in"
  )
RETURN p;
```

- 查询数据中心内设备：反向遍历 `located_in` 路径。
- 查询 POD 成员设备：反向遍历 `relation_kind="member_of"`。
- 查询带有 POD 标签的机柜：反向遍历 `relation_kind="tagged_with"`。
- 未过滤 `relation_kind` 的领域遍历只适用于空间全景探索，不应用于严格物理归属判断。

## 8. 延后范围

当前不建立楼栋、模组、机柜列和逻辑 IDC 节点，不建立子房间树或未证实的空间层级。ROOM 只使用已确认的三种 `located_in` 组合。

## 9. 来源实体

- [device.md](../entities/device.md)
- [cabinet.md](../entities/cabinet.md)
- [data_center.md](../entities/data_center.md)
- [pod.md](../entities/pod.md)
- [room.md](../entities/room.md)
- [rpp.md](../entities/rpp.md)
