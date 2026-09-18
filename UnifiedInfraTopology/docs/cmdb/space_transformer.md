# 数据中心模组信息查询 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/idc_transformer?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：按数据中心变压器信息（可分页、可选字段、可按 idc_id 过滤）。

## 请求体示例
```json
{
  "page": {
    "start": 0,
    "limit": 1000,
    "sort": "inst_id"
  },
  "condition": {
    "inst_id":1588
  }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.idc`：按数据中心 ID 过滤变压器。

## 返回示例
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
        "LVP_busbar": 1,
        "LVP_generator_incoming_id": null,
        "LVP_generator_num": 14,
        "LVP_logic_state": 2,
        "building": 404,
        "city": "廊坊",
        "code": "1T203",
        "create_time": "2024-08-13T13:53:25.913+08:00",
        "creator": "supengwei1",
        "generator_group": null,
        "idc": 451,
        "inst_id": 1588,
        "last_time": "2024-10-31T16:32:33.631+08:00",
        "modifier": "supengwei1",
        "module": 100,
        "obj_id": "idc_transformer",
        "power_in_id": null,
        "standby_transformer": 1593,
        "supplier_account": "0",
        "transformer_brand": "许继",
        "transformer_capacity": 2500,
        "transformer_expire_time": "2025-01-01",
        "transformer_id_tag": "A1-1",
        "transformer_insulation_level": 4,
        "transformer_only_IT": 1,
        "transformer_only_jd": 1,
        "transformer_over_temperature_alarm": 110,
        "transformer_over_temperature_cut": 130,
        "transformer_product_time": "2021-09-01",
        "transformer_type": "SCB11-2500KVA/10",
        "up_Feeder_id": "1G207",
        "up_power_line_id": 708,
        "uuid": "8ff80fda-20d5-4193-b16e-8808d87108a5"
      }
    ]
  }
}
```