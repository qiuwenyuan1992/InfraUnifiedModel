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
    // "idc": 1,
    // "module": 58,
    "inst_id":1156
  }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.idc`：可选， 按数据中心ID 和别的不同用idc来。
- `condition.module`：可选， 按数据中心模组ID 查询UPS模组
- `condition.inst_id`：可选， 按UPS模组的ID查询 



[返回数据]
```json
{
    "result": true,
    "error_code": 0,
    "error_msg": "success",
    "permission": null,
    "data": {
        "count": 1,
        "info": [
            {
                "building": 404,
                "city": "廊坊",
                "code": "F2-UPS-A1",
                "create_time": "2024-08-13T14:53:29.417+08:00",
                "creator": "supengwei1",
                "idc": 451,
                "inst_id": 1156,
                "last_time": "2024-09-03T13:26:35.408+08:00",
                "modifier": "supengwei1",
                "module": 100,
                "obj_id": "idc_ups_group",
                "supplier_account": "0",
                "transformer_id_up": 1588,
                "ups_external_bypass": 1,
                "ups_group_id_tag": "A1-1-1",
                "ups_group_num": 3,
                "ups_only_IT": 1,
                "ups_only_jd": [
                    1
                ],
                "ups_proportion_jd": null,
                "uuid": "1461bad1-8c8b-47c8-ad37-dc12399bdeb4"
            }
        ]
    }
}
```