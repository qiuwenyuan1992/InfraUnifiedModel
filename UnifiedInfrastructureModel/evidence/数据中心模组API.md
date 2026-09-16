# 数据中心模组信息查询 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/idc_module?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：按数据中心查询模组信息（可分页、可选字段、可按 idc_id 过滤）。

## 请求体示例
```json
{
  "fields": [
    "uuid",
    "building_id",
    "idc_building_id",
    "idc_building_name",
    "idc_id",
    "idc_module_id",
    "idc_module_name",
    "idc_name",
    "inst_id"
  ],
  "page": {
    "start": 0,
    "limit": 2,
    "sort": "inst_id"
  },
  "condition": {
    "idc_id": 1
  }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.idc_id`：按数据中心 ID 过滤模组。

## 返回示例（节选，自文件 `数据中心模组.json`）
```json
{
  "result": true,
  "error_code": 0,
  "error_msg": "success",
  "data": {
    "count": 6,
    "info": [
      {
        "inst_id": 31,
        "uuid": "...",
        "idc_id": 1,
        "idc_name": "廊坊_中国联通_磐石",
        "idc_module_id": "M2",
        "idc_module_name": "廊坊_中国联通_磐石_DC5",
        "idc_building_id": "DC5",
        "idc_building_name": "廊坊_中国联通_磐石_DC5",
        "building_id": 411,
        "average_pue": 1.49,
        "JD_use_date": "2021-04-26",
        "use_time": "2021-05-01",
        "de_fire_check": "2023-10-20",
        "de_light_check": "2022-12-17",
        "is_dcim_data": 1,
        "last_time": "2024-07-04T10:24:11.061+08:00",
        "create_time": "2023-12-19T11:33:14.711+08:00",
        "supplier_account": "0"
      }
    ]
  }
}
```

## 字段优先级（建模必选 vs 可选）
- 建模必选（建议入图/入库）
  - 标识：uuid, inst_id
  - 归属：idc_id, idc_name
  - 模组：idc_module_id, idc_module_name
  - 楼宇关联：idc_building_id, idc_building_name, building_id
- 重要可选（有则存储，便于运维/能耗/时间）
  - average_pue, JD_use_date, use_time
  - de_fire_check, de_light_check
  - is_dcim_data, last_time, create_time
- 低优先级（可不建模，需时再取）
  - supplier_account 等无直接拓扑影响的字段

## 字段映射（与拓扑模型）
- 模组节点（Module）：module_id=idc_module_id，module_name=idc_module_name，uuid，inst_id
- 与数据中心关系：Module -[:BELONGS_TO]-> DataCenter(idc_id)
- 与楼宇关系：Module -[:LOCATED_IN]-> Building(building_id / idc_building_id)

## Go 结构体示例（关键字段精简版）
```go
type IDCModuleRequest struct {
    Fields []string `json:"fields"`
    Page   struct {
        Start int    `json:"start"`
        Limit int    `json:"limit"`
        Sort  string `json:"sort"`
    } `json:"page"`
    Condition map[string]interface{} `json:"condition,omitempty"`
}

type IDCModuleResponse struct {
    Result    bool              `json:"result"`
    ErrorCode int               `json:"error_code"`
    ErrorMsg  string            `json:"error_msg"`
    Data      IDCModuleListData `json:"data"`
}

type IDCModuleListData struct {
    Count int          `json:"count"`
    Info  []IDCModule  `json:"info"`
}

type IDCModule struct {
    UUID           string  `json:"uuid"`
    InstID         int     `json:"inst_id"`
    IDCID          int     `json:"idc_id"`
    IDCName        string  `json:"idc_name"`
    ModuleID       string  `json:"idc_module_id"`
    ModuleName     string  `json:"idc_module_name"`
    BuildingID     int     `json:"building_id"`
    IDCBuildingID  string  `json:"idc_building_id"`
    IDCBuildingName string `json:"idc_building_name"`
    AveragePUE     float64 `json:"average_pue"`
    JDUseDate      string  `json:"JD_use_date"`
    UseTime        string  `json:"use_time"`
    DeFireCheck    string  `json:"de_fire_check"`
    DeLightCheck   string  `json:"de_light_check"`
    IsDCIMData     int     `json:"is_dcim_data"`
    LastTime       string  `json:"last_time"`
    CreateTime     string  `json:"create_time"`
}
```

## 调用提示
- 若仅需关键字段，`fields` 中保留必选字段即可减少流量。
- 按 idc_id 过滤可获取指定数据中心的模组；如需全量，去掉 `condition` 并提高 `limit`，循环翻页。