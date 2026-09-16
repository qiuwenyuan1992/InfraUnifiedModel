# 机柜信息查询 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/cabinet?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：查询机柜基础信息，支持分页、按机柜 ID 过滤、按字段裁剪。

## 请求体示例
不裁剪字段（全字段返回）：
```json
{
  "page": {
    "start": 0,
    "limit": 2,
    "sort": "inst_id"
  },
  "condition": {
    "inst_id": 59682
  }
}
```

按需裁剪字段（推荐只取关键字段）：
```json
{
  "fields": [
    "uuid",
    "inst_id",
    "code",
    "idc_id",
    "phy_building_id",
    "phy_room_id",
    "rack_size",
    "u_num",
    "spare_u_num",
    "usable_1u",
    "usable_2u",
    "usable_4u",
    "used_u_num",
    "utilization_ratio",
    "rated_current",
    "maximum_current",
    "max_current",
    "allow_use_current",
    "outlets_10a",
    "outlets_16a",
    "power_state",
    "power_status",
    "ops_status",
    "status",
    "is_delete",
    "device_num",
    "server_num",
    "network_device_num",
    "last_time",
    "updated_time",
    "created_at"
  ],
  "page": {
    "start": 0,
    "limit": 100,
    "sort": "inst_id"
  }
}
```
- `fields`：可选，列出需要返回的字段；不指定时返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.inst_id`：可选，按机柜 inst_id 精确查询；也可按其他条件（如 code）扩展。

## 返回示例（节选，自 `机柜API.json`）
```json
{
  "result": true,
  "error_code": 0,
  "error_msg": "success",
  "data": {
    "count": 86344,
    "info": [
      {
        "uuid": "9cf4a962-bbad-4211-a8fd-6c5d66ecc8b7",
        "inst_id": 1,
        "code": "305-A-01",
        "idc_id": 4,
        "phy_building_id": 7,
        "phy_room_id": 29,
        "rack_size": 8,
        "u_num": 45,
        "spare_u_num": 45,
        "usable_1u": 21,
        "usable_2u": 14,
        "usable_4u": 8,
        "used_u_num": 0,
        "utilization_ratio": 1,
        "rated_current": 20,
        "maximum_current": 32,
        "max_current": 32,
        "allow_use_current": 22,
        "outlets_10a": 25,
        "outlets_16a": 0,
        "power_state": 1,
        "power_status": 0,
        "ops_status": 14,
        "status": "Dissolved",
        "is_delete": 0,
        "device_num": 0,
        "server_num": 0,
        "network_device_num": 0,
        "last_time": "2026-01-19T12:40:39.88+08:00",
        "updated_time": "2025-09-16T21:09:09.322+08:00",
        "created_at": "2018-03-07 08:13:57"
      }
    ]
  }
}
```

## 字段优先级（建模必选 vs 可选）
- 建模必选（建议入图/入库）
  - 标识：uuid, inst_id, code
  - 归属：idc_id, phy_building_id, phy_room_id
  - 规格与容量：rack_size, u_num, spare_u_num, usable_1u/usable_2u/usable_4u, used_u_num, utilization_ratio
  - 供电能力：rated_current, maximum_current/max_current, allow_use_current, outlets_10a/16a
  - 状态：power_state, power_status, ops_status, status, is_delete
  - 设备计数：device_num, server_num, network_device_num
  - 时间：created_at, updated_time, last_time
- 重要可选
  - pdu_num, rest_a_pdu, rest_b_pdu, remain_power, used_rated_power
  - business_model/support_business_model_ids（与业务模型关联时使用）
  - action_id, jira_key（若需追踪变更）
- 低优先级（可不建模，需时再取）
  - 成本/合同类（cost、contract、procurement_*、rent_time 等）
  - 申请/借用/退租时间、组织编码、锁/标签等运营流程字段

## 字段映射（与拓扑模型）
- 机柜节点（Cabinet）：code, inst_id, uuid, idc_id, phy_building_id, phy_room_id
- 容量/占用：u_num, spare_u_num, usable_1u/2u/4u, used_u_num, utilization_ratio, rack_size
- 供电：rated_current, maximum_current/max_current, allow_use_current, outlets_10a/16a, power_state/power_status
- 统计：device_num, server_num, network_device_num
- 状态/时间：ops_status, status, is_delete, created_at, updated_time, last_time
- 关系：Cabinet -[:LOCATED_IN]-> Room(phy_room_id)；Cabinet -[:BELONGS_TO]-> Building(phy_building_id)；可与 RPP/Row 结合时再补充关联。

## Go 结构体示例（关键字段精简版）
```go
type CabinetRequest struct {
    Fields []string `json:"fields,omitempty"`
    Page   struct {
        Start int    `json:"start"`
        Limit int    `json:"limit"`
        Sort  string `json:"sort"`
    } `json:"page"`
    Condition map[string]interface{} `json:"condition,omitempty"`
}

type CabinetResponse struct {
    Result    bool          `json:"result"`
    ErrorCode int           `json:"error_code"`
    ErrorMsg  string        `json:"error_msg"`
    Data      CabinetList   `json:"data"`
}

type CabinetList struct {
    Count int       `json:"count"`
    Info  []Cabinet `json:"info"`
}

type Cabinet struct {
    UUID             string  `json:"uuid"`
    InstID           int     `json:"inst_id"`
    Code             string  `json:"code"`
    IDCID            int     `json:"idc_id"`
    PhyBuildingID    int     `json:"phy_building_id"`
    PhyRoomID        int     `json:"phy_room_id"`
    RackSize         int     `json:"rack_size"`
    UNum             int     `json:"u_num"`
    SpareUNum        int     `json:"spare_u_num"`
    Usable1U         int     `json:"usable_1u"`
    Usable2U         int     `json:"usable_2u"`
    Usable4U         int     `json:"usable_4u"`
    UsedUNum         int     `json:"used_u_num"`
    UtilizationRatio float64 `json:"utilization_ratio"`
    RatedCurrent     int     `json:"rated_current"`
    MaximumCurrent   int     `json:"maximum_current"`
    MaxCurrent       int     `json:"max_current"`
    AllowUseCurrent  int     `json:"allow_use_current"`
    Outlets10A       int     `json:"outlets_10a"`
    Outlets16A       int     `json:"outlets_16a"`
    PowerState       int     `json:"power_state"`
    PowerStatus      int     `json:"power_status"`
    OpsStatus        int     `json:"ops_status"`
    Status           string  `json:"status"`
    IsDelete         int     `json:"is_delete"`
    DeviceNum        int     `json:"device_num"`
    ServerNum        int     `json:"server_num"`
    NetworkDeviceNum int     `json:"network_device_num"`
    CreatedAt        string  `json:"created_at"`
    UpdatedTime      string  `json:"updated_time"`
    LastTime         string  `json:"last_time"`
}
```

## 调用提示
- 若仅需关键字段，`fields` 保留必选字段，可显著减小返回体。
- 批量获取时提高 `limit`（如 500/1000），循环翻页。
- 若需按 code/room/building 过滤，可在 `condition` 中增加对应字段。