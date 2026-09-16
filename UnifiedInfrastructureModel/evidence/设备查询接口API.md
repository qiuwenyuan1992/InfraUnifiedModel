# 设备查询接口 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/device_view?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：查询设备（含服务器/网络设备等）信息，支持简单条件与复杂条件筛选，可按字段裁剪。

## 请求体示例
### 简单条件
```json
{
  "fields": [],
  "page": { "start": 0, "limit": 20, "sort": "-inst_id" },
  "condition": {
    "device_sn": "G1B3CKT000010",   // 可选，按设备 SN
    "idc_cabinet_id": 103195       // 可选，按机柜 ID
  }
}
```

### 复杂条件（多规则 AND/OR 组合）
```json
{
  "fields": [],
  "page": { "start": 0, "limit": 10, "sort": "-inst_id" },
  "conditions": {
    "condition": "AND",
    "rules": [
      { "field": "parent_type_id", "operator": "in", "value": [54] }, // 设备类型
      { "field": "idc_id", "operator": "in", "value": [1] },         // 数据中心
      { "field": "tree_asset_organization_code", "operator": "in", "value": ["00000000","00111732"] },
      { "field": "tree_asset_id", "operator": "not_in", "value": [6,7,9] }
    ]
  }
}
```
- `fields`：可选，声明需要返回的字段；不指定则可能返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition` / `conditions`：单条件或组合条件筛选。

## 返回示例（节选，自 `设备查询接口API.json`）
```json
{
  "result": true,
  "error_code": 0,
  "error_msg": "success",
  "data": {
    "count": 2,
    "info": [
      {
        "inst_id": 732778,
        "uuid": "4ca21ef7-189e-d63c-d796-5332ab3d5dea",
        "device_sn": "G1B3CKT000010",
        "device_type": "盒式交换机",
        "device_type_id": 190,
        "parent_type": "网络设备",
        "parent_type_id": 54,
        "brand": "RG-S6990",
        "model": "RG-S6990-128QC2XS",
        "manufacturer": "RUIJIE",
        "idc_id": 1,
        "idc_name": "廊坊_中国联通_磐石",
        "idc_module_id": 124,
        "idc_module_name": "廊坊_中国联通_磐石_DC15",
        "idc_cabinet_id": 103195,
        "cabinet": "DC15-438-G02",
        "room_id": 3031,
        "room": "438",
        "building_id": 1014,
        "building_name": "廊坊磐石DC15",
        "u_start": 42,
        "u_end": 45,
        "u_position": "42--45",
        "height": 4,
        "device_power_status": 0,
        "device_on_off": 0,
        "operation": "在线",
        "operation_id": 1,
        "service_status": "建设完成已交付",
        "service_status_id": 8,
        "service": "DOC",
        "business": "商城",
        "business_id": "53",
        "op_group": ["JC_NETWORK"],
        "op_group_id": [27],
        "device_order": "XZC20250104272",
        "arrival_at": "2025-08-18 00:00:00",
        "maintenance_start": "2025-11-12 00:00:00",
        "maintenance_end": "2030-11-11 00:00:00",
        "create_time": "2026-01-21T17:45:50.108+08:00",
        "last_time": "2026-01-21T17:45:50.108+08:00",
        "power": 3019,
        "idc_cabinet": "DC15-438-G02",
        "rack_name": "G02",
        "pod_id": 2142,
        "pod_name": "POD083",
        "eth_ip": ["11.97.0.72"],
        "ilo_ip": ["11.96.0.72"],
        "host_name": "LFG25_DC15-438-G02_POD083_T1_MAL_001",
        "tag": "使用中"
      }
    ]
  }
}
```

## 字段优先级（建模必选 vs 可选）
- 建模必选（建议入图/入库）
  - 标识：inst_id, uuid, device_sn
  - 类型：device_type/device_type_id，parent_type/parent_type_id
  - 型号与品牌：brand, model, manufacturer
  - 归属：idc_id/idc_name，idc_module_id/idc_module_name，idc_cabinet_id/cabinet，room_id/room，building_id/building_name
  - 机柜位置信息：u_start, u_end, u_position, height
  - 运行/电源状态：operation/operation_id，device_power_status，device_on_off，service_status/service_status_id，tag
  - 网络标识：eth_ip，ilo_ip，host_name
  - 业务/责任：service, business/business_id，op_group/op_group_id
  - 时间：create_time，last_time，arrival_at
- 重要可选
  - 功耗/电气：power，cabinet_rated_current
  - 维护：maintenance_start/maintenance_end/maintenance_status
  - 订单/资产：device_order，ad_asset_number/op_asset_number
  - 位置增强：pod_id/pod_name，building_uuid/room_uuid
- 低优先级（可不建模，需时再取）
  - 大量财务/部门/监控细节字段、parts_*、pkg_*、organization_fullpath 等

## 字段映射（与拓扑模型）
- Device 节点：inst_id, uuid, device_sn, device_type/id, brand, model, manufacturer, operation, device_power_status, host_name, eth_ip, ilo_ip, power
- 归属关系：
  - Device -[:LOCATED_IN]-> Cabinet(idc_cabinet_id)
  - Device -[:IN_ROOM]-> Room(room_id)
  - Device -[:IN_BUILDING]-> Building(building_id)
  - Device -[:IN_MODULE]-> Module(idc_module_id)
  - Device -[:IN_DATACENTER]-> DataCenter(idc_id)
- 位置信息：u_start/u_end/u_position/height，用于机柜内布局
- 业务/责任：service/business/op_group 可映射到业务/运维责任关系（按需）

## Go 结构体示例（关键字段精简版）
```go
type DeviceRequest struct {
    Fields     []string                `json:"fields,omitempty"`
    Page       struct {
        Start int    `json:"start"`
        Limit int    `json:"limit"`
        Sort  string `json:"sort"`
    } `json:"page"`
    Condition  map[string]interface{}  `json:"condition,omitempty"`
    Conditions map[string]interface{}  `json:"conditions,omitempty"` // 复杂规则
}

type DeviceResponse struct {
    Result    bool        `json:"result"`
    ErrorCode int         `json:"error_code"`
    ErrorMsg  string      `json:"error_msg"`
    Data      DeviceList  `json:"data"`
}

type DeviceList struct {
    Count int      `json:"count"`
    Info  []Device `json:"info"`
}

type Device struct {
    InstID        int      `json:"inst_id"`
    UUID          string   `json:"uuid"`
    DeviceSN      string   `json:"device_sn"`
    DeviceType    string   `json:"device_type"`
    DeviceTypeID  int      `json:"device_type_id"`
    ParentType    string   `json:"parent_type"`
    ParentTypeID  int      `json:"parent_type_id"`
    Brand         string   `json:"brand"`
    Model         string   `json:"model"`
    Manufacturer  string   `json:"manufacturer"`
    IDCID         int      `json:"idc_id"`
    IDCName       string   `json:"idc_name"`
    ModuleID      int      `json:"idc_module_id"`
    ModuleName    string   `json:"idc_module_name"`
    CabinetID     int      `json:"idc_cabinet_id"`
    Cabinet       string   `json:"cabinet"`
    RoomID        int      `json:"room_id"`
    Room          string   `json:"room"`
    BuildingID    int      `json:"building_id"`
    BuildingName  string   `json:"building_name"`
    UStart        int      `json:"u_start"`
    UEnd          int      `json:"u_end"`
    UPosition     string   `json:"u_position"`
    Height        int      `json:"height"`
    Operation     string   `json:"operation"`
    OperationID   int      `json:"operation_id"`
    DevicePowerStatus int  `json:"device_power_status"`
    DeviceOnOff   int      `json:"device_on_off"`
    ServiceStatus string   `json:"service_status"`
    ServiceStatusID int    `json:"service_status_id"`
    Service       string   `json:"service"`
    Business      string   `json:"business"`
    BusinessID    string   `json:"business_id"`
    OpGroup       []string `json:"op_group"`
    OpGroupID     []int    `json:"op_group_id"`
    HostName      string   `json:"host_name"`
    EthIP         []string `json:"eth_ip"`
    IloIP         []string `json:"ilo_ip"`
    Power         int      `json:"power"`
    ArrivalAt     string   `json:"arrival_at"`
    MaintenanceStart string `json:"maintenance_start"`
    MaintenanceEnd   string `json:"maintenance_end"`
    CreateTime    string   `json:"create_time"`
    LastTime      string   `json:"last_time"`
    Tag           string   `json:"tag"`
}
```

## 调用提示
- 仅需关键字段时，`fields` 保留必选字段可显著减小返回体。
- 大批量查询时提高 `limit`（如 500/1000），结合 `conditions` 做过滤并循环翻页。
- 精确定位设备可组合 device_sn + idc_cabinet_id；按类型/业务/机房/楼宇等用 `conditions` 组合筛选。