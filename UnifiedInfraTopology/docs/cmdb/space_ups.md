# 数据中心的UPS设备API（关联到UPS组）

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/idc_ups?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：查询UPS设备的信息（可分页、可选字段、可按 idc、module、inst_id 过滤）。

## 请求体示例
```json
{
    "page": {
        "start": 0,
        "limit": 1000,
        "sort": "inst_id"
    },
    "condition": {
        "idc": 451,
        "module": 100,
        "transformer_id_up":1588,
        "ups_group":1156
    }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.idc`：可选， 按数据中心ID 和别的不同用idc来。
- `condition.module`：可选， 按数据中心模组ID 查询UPS模组
- `condition.ups_group`：可选， 按UPS模组的ID查询 



[返回数据]

```json
{
    "result": true,
    "error_code": 0,
    "error_msg": "success",
    "permission": null,
    "data": {
        "count": 3,
        "info": [
            {
                "battery_brand": "山东圣阳",
                "battery_group_num": 4,
                "battery_num": 40,
                "battery_product_time": "2021-09-01",
                "battery_resistance": 3.6,
                "battery_type": "SPG12-490W",
                "battery_voltage": 12,
                "building": 404,
                "city": "廊坊",
                "code": "F2-UPS-A12",
                "create_time": "2024-08-13T15:04:29.95+08:00",
                "creator": "supengwei1",
                "idc": 451,
                "inst_id": 3255,
                "last_time": "2026-06-10T16:57:31.43+08:00",
                "modifier": "supengwei1",
                "module": 100,
                "obj_id": "idc_ups",
                "supplier_account": "0",
                "transformer_id_up": 1588,
                "ups_brand": "维谛",
                "ups_capacitor_change_date": null,
                "ups_capacity": 500,
                "ups_exprie_date": "2024-12-01",
                "ups_group": 1156,
                "ups_group_id_tag": "A",
                "ups_product_date": "2021-09-01",
                "ups_type": "EXL S1",
                "uuid": "ddc7cfc5-1b84-43e9-8ba7-78685d38f4b1"
            },
            {
                "battery_brand": "山东圣阳",
                "battery_group_num": 4,
                "battery_num": 40,
                "battery_product_time": "2021-09-01",
                "battery_resistance": 3.6,
                "battery_type": "SPG12-490W",
                "battery_voltage": 12,
                "building": 404,
                "city": "廊坊",
                "code": "F2-UPS-A11",
                "create_time": "2024-08-13T15:04:29.973+08:00",
                "creator": "supengwei1",
                "idc": 451,
                "inst_id": 3280,
                "last_time": "2026-06-10T16:57:29.848+08:00",
                "modifier": "supengwei1",
                "module": 100,
                "obj_id": "idc_ups",
                "supplier_account": "0",
                "transformer_id_up": 1588,
                "ups_brand": "维谛",
                "ups_capacitor_change_date": null,
                "ups_capacity": 500,
                "ups_exprie_date": "2024-12-01",
                "ups_group": 1156,
                "ups_group_id_tag": "A",
                "ups_product_date": "2021-09-01",
                "ups_type": "EXL S1",
                "uuid": "be8db0e1-6624-4bbd-9113-b1b894385f65"
            },
            {
                "battery_brand": "山东圣阳",
                "battery_group_num": 4,
                "battery_num": 40,
                "battery_product_time": "2021-09-01",
                "battery_resistance": 3.6,
                "battery_type": "SPG12-490W",
                "battery_voltage": 12,
                "building": 404,
                "city": "廊坊",
                "code": "F2-UPS-A13",
                "create_time": "2024-08-13T15:04:30.016+08:00",
                "creator": "supengwei1",
                "idc": 451,
                "inst_id": 3370,
                "last_time": "2026-06-10T16:57:31.699+08:00",
                "modifier": "supengwei1",
                "module": 100,
                "obj_id": "idc_ups",
                "supplier_account": "0",
                "transformer_id_up": 1588,
                "ups_brand": "维谛",
                "ups_capacitor_change_date": null,
                "ups_capacity": 500,
                "ups_exprie_date": "2024-12-01",
                "ups_group": 1156,
                "ups_group_id_tag": "A",
                "ups_product_date": "2021-09-01",
                "ups_type": "EXL S1",
                "uuid": "72d59d8d-74bc-4230-b236-27e1e6527f8f"
            }
        ]
    }
}
```