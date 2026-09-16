# 数据中心UPS组API（将多个UPS设备编组，关联列头柜）

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/idc_ups_group?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：按数据中心查询UPS组信息信息（可分页、可选字段、可按 idc、module、inst_id 过滤）。

## 请求体示例
```json
{
  "page": {
    "start": 0,
    "limit": 2,
    "sort": "inst_id"
  },
  "condition": {
    "idc": 1,
    "module": 58,
    "inst_id":111
  }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.idc`：可选， 按数据中心ID 和别的不同用idc来。
- `condition.module`：可选， 按数据中心模组ID 查询UPS模组
- `condition.inst_id`：可选， 按UPS模组的ID查询 



[返回数据](topo_doc/数据中心电力-UPS组.json)