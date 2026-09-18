数据中心UPS组下联的列头柜API（多个UPS设备编组，构成UPS组，UPS组下联列头柜）
基本信息
接口地址：http://cmdb.jd.com/pub/api/v3/find/instance/object/idc_RPP?user=cmdb_all&timestamp=1&auth=1
请求方法：POST
请求体类型：application/json
功能：查询列头柜的信息（可分页、可选字段、可按 idc、module、inst_id 过滤）。
请求体示例
{
   
    "page": {
        "start": 0,
        "limit": 1,
        "sort": "inst_id"
    },
    "condition":{
        "ups_group":720
    }
}
fields：可选，列出需要返回的字段；不指定时可返回全字段。
page.start / page.limit / page.sort：分页与排序。
condition.idc：可选， 按数据中心ID 和别的不同用idc来。
condition.module：可选， 按数据中心模组ID 查询UPS模组
condition.inst_id：可选， 按UPS模组的ID查询
condition.ups_group：可选， 按UPS组的id查询下联的列头柜 ups_group

返回数据

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
                "code": "F2_IT06AC-8A",
                "create_time": "2024-08-13T15:24:52.367+08:00",
                "creator": "supengwei1",
                "idc": 451,
                "idc_room": 889,
                "inst_id": 6204,
                "last_time": "2024-08-20T17:04:04.946+08:00",
                "modifier": "supengwei1",
                "module": 100,
                "obj_id": "idc_RPP",
                "row_in_num": 18,
                "row_power_type": 1,
                "row_rated_current": 160,
                "row_rated_voltage": 400,
                "supplier_account": "0",
                "transformer_group_a": 1588,
                "ups_group": 1156,
                "uuid": "9f283fc3-d029-4a4d-b312-d23b5c2c4cd5"
            }
        ]
    }
}
```