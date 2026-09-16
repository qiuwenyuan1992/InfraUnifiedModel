# GPU 可观测拓扑设计方案

## 一、背景

基础设施全栈可观测需覆盖GPU集群。当前Neo4j拓扑仅有物理视图（DataCenter→Building→Room→Cabinet→Device），缺少GPU集群的逻辑分组视图。DCGM指标数据存储在ClickHouse中，需要与拓扑关联。

## 二、核心设计

### 2.1 ComputePod 节点（新增）

ComputePod = GPU训练集群（逻辑概念，跨机柜跨机房）。

```
物理视图（已有）：  DataCenter → Building → Room → Cabinet → Device
逻辑视图（新增）：  DataCenter → ComputePod → Device（同一个Device节点）
```

一台设备同时拥有两条归属关系：
- `(:Cabinet)-[:CONTAIN]->(:Device)` — 物理位置
- `(:ComputePod)-[:CONTAINS]->(:Device)` — 逻辑集群

### 2.2 Device 增加 compute_pod_id 属性

同步设备时从CMDB获取 compute_pod_id，写入Neo4j Device节点属性。这样即使不通过ComputePod节点，也可直接按属性查询：

```cypher
MATCH (d:Device {compute_pod_id: "POD011"}) RETURN d
```

### 2.3 网络层级（T0/T1/T2）

| 层级 | 含义 | 存储策略 |
|------|------|----------|
| T0 | 接入层（Leaf），连接GPU服务器 | **已存储** CONNECTED_TO 关系 |
| T1 | 汇聚层（Spine） | **不存储**，分析时按需查询 |
| T2 | 核心层（Super Spine） | **不存储**，分析时按需查询 |

原因：实际数据走哪个T1/T2链路不确定，且全量存储复杂无必要。

### 2.4 DCGM 数据关联

ClickHouse中DCGM指标**没有compute_pod_id**，只有IP地址。

关联路径：
```
DCGM IP → Neo4j Device(eth_ip/ilo_ip) → compute_pod_id → ComputePod
```

实现方案：Go后端维护 IP→{device_sn, compute_pod_id} 缓存（Redis或内存），定期从Neo4j刷新。

## 三、Neo4j 节点设计

### ComputePod 节点属性

```cypher
(:ComputePod {
  compute_pod_id: "POD011",       // 唯一标识（CMDB inst_id 或 basic_code）
  name: "POD011",                 // 短名称
  full_name: "宿迁湖滨新区T2-POD011",  // 全名
  basic_code: "SQV05-POD011",    // CMDB编码
  inst_id: 944,                   // CMDB实例ID
  idc_logic_id: 28,              // 逻辑数据中心ID
  mode: "L3",                     // 网络模式
  rdma: 2                         // RDMA类型
})
```

### 关系设计

```cypher
(:DataCenter)-[:HAS_COMPUTE_POD]->(:ComputePod)
(:ComputePod)-[:CONTAINS]->(:Device {compute_pod_id: "POD011"})
(:Device {role:"gpu_server"})-[:CONNECTED_TO]->(:Device {role:"T0"})  // 已有
```

## 四、数据源 API

### Pod 列表 API
- URL: `http://cmdb.jd.com/pub/api/v3/find/instance/object/pod?user=cmdb_all&timestamp=1&auth=1`
- Method: POST
- 参数: `{ "fields": [], "page": {"start":0,"limit":10,"sort":"inst_id"}, "condition": {"plane": 2} }`
- plane=2 表示计算面（GPU集群）
- 返回: inst_id, basic_code, full_name, name, idc_logic_id, mode, rdma, pod_uuid 等

### 设备查询（已有，需增加字段）
- 需在 Fields 中增加 `compute_pod_id`
- 同步时写入 `d.compute_pod_id = device.compute_pod_id`

## 五、同步改动清单

| 文件 | 改动 |
|------|------|
| `topo/model/device.go` | HostDevice 结构体增加 ComputePodID 字段 |
| `topo/sync/host_device.go` | Fields 增加 "compute_pod_id"，MERGE SET 增加 d.compute_pod_id |
| `topo/sync/compute_pod_sync.go`（新增） | 调Pod API(plane=2)，创建ComputePod节点，建立CONTAINS关系 |
| `topo/model/compute_pod.go`（新增） | ComputePod 请求/响应结构体 |

## 六、查询示例

```cypher
-- 查某数据中心下所有 ComputePod
MATCH (dc:DataCenter {idc_id: 1})-[:HAS_COMPUTE_POD]->(pod:ComputePod)
RETURN pod

-- 查 Pod 下所有设备 + T0网络连接
MATCH (d:Device {compute_pod_id: "POD011"})
OPTIONAL MATCH (d)-[r:CONNECTED_TO]->(sw:Device)
RETURN d, r, sw

-- DCGM IP 反查设备与Pod
MATCH (d:Device) WHERE $ip IN d.eth_ip OR $ip IN d.ilo_ip
RETURN d.compute_pod_id, d.device_sn, d.eth_ip_str
```

## 七、参考项目

topology_grid (`/Users/qiuwenyuan/Documents/workspace/JD/topology_grid`)：
- 使用 NebulaGraph，DevNode 包含 Pod/PodUuid 字段
- Edge类型：Server→Device(T0)=1, Device→Device(同Role)=2, Device→Device(跨Role)=3
- GPU故障定位：module/controller/gpu/gpu.go
- ClickHouse模型：model/clickhouse/ 目录

## 八、待确认事项

- [ ] CMDB设备查询是否直接返回 compute_pod_id（需确认字段名是否为 compute_pod_id 或 pod_id）
- [ ] Pod API 是否返回包含的设备列表，还是需要反查设备的 compute_pod_id
- [ ] ClickHouse DCGM 表结构（字段名、时间精度、指标类型）
- [ ] 是否需要维护 ComputePod→DataCenter 的反向关系（通过 idc_logic_id 映射）