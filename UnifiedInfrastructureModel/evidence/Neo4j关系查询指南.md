# Neo4j关系查询指南

本文档提供了数据中心拓扑关系中各种查询的Cypher语句示例。

## 1. 基础关系查询

### 1.1 模组与数据中心关系
```cypher
MATCH (m:Module)-[r:BELONGS_TO]->(d:DataCenter)
RETURN m.name as module_name, d.name as dc_name
LIMIT 5
```

### 1.2 空间拓扑关系
```cypher
MATCH (dc:DataCenter)-[:CONTAINS]->(b:Building)-[:CONTAINS]->(r:Room)-[:CONTAINS]->(c:Cabinet)
RETURN dc.name as dc_name, b.name as building_name, r.name as room_name, c.name as cabinet_name
LIMIT 5
```

### 1.3 电力拓扑关系
```cypher
MATCH (dc:DataCenter)-[:HAS_UPS_GROUP]->(ug:UPSGroup)-[:HAS_UPS_DEVICE]->(ud:UPSDevice)-[:HAS_REMOTE_POWER_PANEL]->(rpp:RemotePowerPanel)
RETURN dc.name as dc_name, ug.name as ups_group_name, ud.name as ups_device_name, rpp.name as rpp_name
LIMIT 5
```

## 2. 设备相关查询

### 2.1 设备与机柜关系
```cypher
MATCH (d:Device)-[:CONTAINED_IN]->(c:Cabinet)
RETURN d.name as device_name, d.parent_type as device_type, c.name as cabinet_name
LIMIT 10
```

### 2.2 服务器设备查询
```cypher
MATCH (d:Device {parent_type: "server"})
RETURN d.name as name, d.ip as ip, d.role as role
```

### 2.3 网络设备查询
```cypher
MATCH (d:Device {parent_type: "network_device"})
RETURN d.name as name, d.device_type as type, d.vendor as vendor
```

## 3. 网络连接关系

### 3.1 网络连接查询
```cypher
MATCH (d1:Device)-[r:CONNECTED_TO]->(d2:Device)
RETURN d1.name as device1_name, r.port1 as port1, r.ip1 as ip1, 
       d2.name as device2_name, r.port2 as port2, r.ip2 as ip2
LIMIT 5
```

### 3.2 特定设备的连接关系
```cypher
MATCH (d:Device)-[:CONNECTED_TO]-(connected:Device)
WHERE d.name = "目标设备名称"
RETURN d.name as device, collect(connected.name) as connected_devices
```

## 4. 电力供应关系

### 4.1 机柜电力供应查询
```cypher
MATCH (rpp:RemotePowerPanel)-[:POWER_SUPPLY]->(c:Cabinet)
RETURN rpp.name as rpp_name, c.name as cabinet_name
LIMIT 10
```

### 4.2 完整电力路径查询
```cypher
MATCH path = (dc:DataCenter)-[:HAS_UPS_GROUP]->(:UPSGroup)-[:HAS_UPS_DEVICE]->(:UPSDevice)-[:HAS_REMOTE_POWER_PANEL]->(:RemotePowerPanel)-[:POWER_SUPPLY]->(c:Cabinet)
WHERE c.name = "目标机柜名称"
RETURN [node in nodes(path) | node.name] as power_path
```

## 5. 统计查询

### 5.1 数据中心统计信息
```cypher
MATCH (dc:DataCenter)
OPTIONAL MATCH (dc)-[:CONTAINS]->(b:Building)
OPTIONAL MATCH (b)-[:CONTAINS]->(r:Room)
OPTIONAL MATCH (r)-[:CONTAINS]->(c:Cabinet)
OPTIONAL MATCH (c)<-[:CONTAINED_IN]-(d:Device)
RETURN dc.name as dc_name,
       count(DISTINCT b) as building_count,
       count(DISTINCT r) as room_count,
       count(DISTINCT c) as cabinet_count,
       count(DISTINCT d) as device_count
```

### 5.2 电力系统统计
```cypher
MATCH (dc:DataCenter)
OPTIONAL MATCH (dc)-[:HAS_UPS_GROUP]->(ug:UPSGroup)
OPTIONAL MATCH (ug)-[:HAS_UPS_DEVICE]->(ud:UPSDevice)
OPTIONAL MATCH (ud)-[:HAS_REMOTE_POWER_PANEL]->(rpp:RemotePowerPanel)
RETURN dc.name as dc_name,
       count(DISTINCT ug) as ups_group_count,
       count(DISTINCT ud) as ups_device_count,
       count(DISTINCT rpp) as rpp_count
```

### 5.3 设备类型分布
```cypher
MATCH (d:Device)
RETURN d.parent_type as device_type, count(d) as count
ORDER BY count DESC
```

## 6. 高级查询

### 6.1 完整拓扑路径
```cypher
MATCH path = (dc:DataCenter)-[:CONTAINS*]->(c:Cabinet)
RETURN [node in nodes(path) | node.name] as path_names
LIMIT 10
```

### 6.2 未连接的设备
```cypher
MATCH (d:Device)
WHERE NOT (d)-[:CONNECTED_TO]-()
RETURN d.name as name, d.parent_type as type
```

### 6.3 高连接度设备
```cypher
MATCH (d:Device)-[:CONNECTED_TO]-(connected:Device)
WITH d, count(connected) as connection_count
WHERE connection_count > 5
RETURN d.name as name, d.parent_type as type, connection_count
ORDER BY connection_count DESC
```

### 6.4 特定机柜内的所有设备
```cypher
MATCH (c:Cabinet {name: "目标机柜名称"})<-[:CONTAINED_IN]-(d:Device)
RETURN d.name as device_name, d.parent_type as device_type, d.role as role
```

## 7. 关系类型总结

| 关系类型 | 描述 | 方向 |
|---------|------|------|
| BELONGS_TO | 模组属于数据中心 | Module -> DataCenter |
| CONTAINS | 空间包含关系 | 父级 -> 子级 |
| HAS_UPS_GROUP | 数据中心拥有UPS组 | DataCenter -> UPSGroup |
| HAS_UPS_DEVICE | UPS组拥有UPS设备 | UPSGroup -> UPSDevice |
| HAS_REMOTE_POWER_PANEL | UPS设备拥有列头柜 | UPSDevice -> RemotePowerPanel |
| POWER_SUPPLY | 列头柜供电给机柜 | RemotePowerPanel -> Cabinet |
| CONTAINED_IN | 设备在机柜内 | Device -> Cabinet |
| CONNECTED_TO | 设备间网络连接 | Device -> Device |

## 8. 节点类型总结

| 节点类型 | 描述 | 关键属性 |
|---------|------|----------|
| DataCenter | 数据中心 | name, dc_id |
| Module | 模组 | name, inst_id |
| Building | 楼宇 | name, building_id |
| Room | 房间 | name, room_id |
| Cabinet | 机柜 | name, cabinet_id, height, width, depth |
| UPSGroup | UPS组 | name, group_id |
| UPSDevice | UPS设备 | name, device_id, capacity |
| RemotePowerPanel | 列头柜 | name, panel_id, capacity |
| Device | 设备 | name, device_id, parent_type, ip, role |

## 9. 使用示例

### 9.1 查询特定数据中心的完整拓扑
```cypher
MATCH (dc:DataCenter {name: "目标数据中心"})
OPTIONAL MATCH (dc)-[:CONTAINS]->(b:Building)
OPTIONAL MATCH (b)-[:CONTAINS]->(r:Room)
OPTIONAL MATCH (r)-[:CONTAINS]->(c:Cabinet)
OPTIONAL MATCH (c)<-[:CONTAINED_IN]-(d:Device)
RETURN dc.name as dc_name, 
       collect(DISTINCT {building: b.name, rooms: collect(DISTINCT {room: r.name, cabinets: collect(DISTINCT {cabinet: c.name, devices: collect(DISTINCT d.name)})})}) as topology
```

### 9.2 查询设备故障影响范围
```cypher
MATCH (failed_device:Device {name: "故障设备"})
MATCH (failed_device)-[:CONNECTED_TO*1..3]-(affected:Device)
RETURN failed_device.name as failed_device, collect(DISTINCT affected.name) as affected_devices
```

### 9.3 查询电力故障影响范围
```cypher
MATCH (failed_rpp:RemotePowerPanel {name: "故障列头柜"})
MATCH (failed_rpp)-[:POWER_SUPPLY]->(affected_cabinet:Cabinet)
MATCH (affected_cabinet)<-[:CONTAINED_IN]-(affected_device:Device)
RETURN failed_rpp.name as failed_rpp, 
       collect(DISTINCT affected_cabinet.name) as affected_cabinets,
       collect(DISTINCT affected_device.name) as affected_devices