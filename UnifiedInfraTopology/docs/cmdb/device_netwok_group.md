# 网络端口组链路 API 文档

## 基本信息
- 接口地址：`http://test.cmdb.jd.com/pub/api/v3/find/instance/object/port_group?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：网络端口组链路（可分页、可选字段、可按 idc_id 过滤）。

## 请求体示例

### 先按稳定 UUID 查询本端端口组

```json
{
    "page": {
        "start": 0,
        "limit": 100,
        "sort": "inst_id"
    },
    "condition": {
        "uuid": "bd30c202-4f2f-579e-64a5-5527e5d4d815"
    }
}
```

### 再按返回的链路 ID 查询同一链路成员

```json
{
    "page": {
        "start": 0,
        "limit": 100,
        "sort": "inst_id"
    },
    "condition": {
        "port_group_link_id": 2328
    }
}
```

- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.uuid`：按稳定端口组成员 UUID 查询。
- `condition.inst_id`：按端口组成员数字 ID 查询；已有 UUID 时不需要使用。
- `condition.port_group_link_id`：查询同一端口组链路下的全部成员。
- API 支持灵活条件查询；同步模型不需要保存 `group_id` 或 `port_group_id`。返回的 `port_group_link_id` 只用于当前请求继续查询成员。

## 返回字段补充

- `uuid`：本端端口组成员 UUID，对应 `port_view.port_group_uuid`。
- `inst_id`：本端端口组成员数字 ID，对应 `port_view.port_group_id`，不进入目标模型。
- `port_group_link_id`：端口组链路数字 ID，对应 `port_view.group_id`，不进入目标模型。
- `group_uuid`：链路稳定 UUID 来自 `port_view.group_uuid`，保存在接口属性中。
- `group_type`：端口组一级类型；3=inner_link（机房内端口组）、1=DCI（专线）、2=POP（出口）。三种类型统一使用本接口查询。

## 返回示例
```json
{
    "result": true,
    "error_code": 0,
    "error_msg": "success",
    "permission": null,
    "data": {
        "count": 2,
        "info": [
            {
                "action_id": "582154815063240704",
                "cn_name": "廊坊润惠1#_POD006_CORE-Border(大数据)",
                "created_at": "2022-05-26 10:35:21",
                "deleted_at": "2000-01-01 00:00:00",
                "enable_time": "2022-05-26 10:35:21",
                "id": 4212,
                "inst_id": 4212,
                "is_delete": 0,
                "is_sync": 0,
                "last_time": "2026-08-21T11:07:53.67+08:00",
                "logic_datacenter_id": 0,
                "logic_datacenter_name": "",
                "logic_datacenter_uuid": "",
                "modifier": "wangyanjie25",
                "name": "廊坊润惠1#_POD006_CORE-Border(大数据)",
                "obj_id": "port_group",
                "phy_building_id": 404,
                "physics_bandwidth": 25600000,
                "pod_id": 914,
                "port_group_link_id": 2328,
                "role": "T1-T2",
                "sid": "廊坊润惠1#_POD006_CORE-Border(大数据)",
                "supplier_account": "0",
                "support_ids": [
                    54
                ],
                "type": 13,
                "updated_at": "2022-05-26 10:35:23",
                "username": "unknow",
                "uuid": "bd30c202-4f2f-579e-64a5-5527e5d4d815"
            },
            {
                "action_id": "708373955994624000",
                "cn_name": "廊坊润惠1#_POD006_Border-CORE(大数据)",
                "created_at": "2022-05-26 10:35:22",
                "deleted_at": "2000-01-01 00:00:00",
                "enable_time": "2022-05-26 10:35:22",
                "id": 4213,
                "inst_id": 4213,
                "is_delete": 0,
                "is_sync": 0,
                "logic_datacenter_id": 0,
                "logic_datacenter_name": "",
                "logic_datacenter_uuid": "",
                "name": "廊坊润惠1#_POD006_Border-CORE(大数据)",
                "obj_id": "port_group",
                "phy_building_id": 404,
                "physics_bandwidth": 25600000,
                "pod_id": 0,
                "port_group_link_id": 2328,
                "role": "T2-T1",
                "sid": "廊坊润惠1#_POD006_Border-CORE(大数据)",
                "supplier_account": "0",
                "support_ids": [
                    54
                ],
                "type": 13,
                "updated_at": "2023-05-09 17:45:12",
                "username": "guoyanan10",
                "uuid": "4d13f2cb-aa7c-b5fa-392a-30c0be72207d"
            }
        ]
    }
}
```