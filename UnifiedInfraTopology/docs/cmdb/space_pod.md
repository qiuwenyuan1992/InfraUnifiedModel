# POD信息查询 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/pod?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：POD基础信息列表（支持分页与字段选择）。

## 请求体字段
```json
{
    "fields":[
    
    ],
    "page": {
        "start": 0,
        "limit": 2,
        "sort": "inst_id"
    },
    "condition":{
        // "plane":2 //平面类型 管理面(1, 默认值) 计算面(2) 存储面(3) 带外管理面(4)
        "inst_id":2289
    }

}
```

## 返回数据 
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
                "action_id": "",
                "basic_code": "ZWU11-POD246",
                "create_time": "2026-05-26T19:16:37.368+08:00",
                "created_at": "",
                "creator": "baikang1",
                "describe": "",
                "full_name": "ZWU11-POD246",
                "idc_logic_id": 1088,
                "inst_id": 2289,
                "lables": [
                    0
                ],
                "last_time": "2026-05-26T19:17:42.408+08:00",
                "mode": "",
                "modifier": "baikang1",
                "name": "POD246",
                "obj_id": "pod",
                "phy_building_id": 1036,
                "plane": 2,
                "rdma": 1,
                "supplier_account": "0",
                "support_service_ids": [
                    50
                ],
                "updated_at": "",
                "username": "",
                "uuid": "764f199c-c1e9-44f0-b948-82719f516a09"
            }
        ]
    }
}
```

### POD  inst_id = 914
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
                "action_id": "493836217799999488",
                "basic_code": "LFG21-POD006",
                "created_at": "2021-09-24 17:28:49",
                "creator": "",
                "deleted_at": "2000-01-01 00:00:00",
                "describe": "",
                "full_name": "廊坊润惠1#-POD006",
                "id": 914,
                "idc_logic_id": 2,
                "inst_id": 914,
                "is_delete": 0,
                "is_sync": 0,
                "lables": 0,
                "last_time": "2025-08-08T17:10:02.598+08:00",
                "mode": "L3",
                "modifier": "chendongxia3",
                "name": "POD006",
                "obj_id": "pod",
                "phy_building_id": 404,
                "plane": 1,
                "rdma": 0,
                "supplier_account": "0",
                "support_ids": [
                    53
                ],
                "support_service_ids": [
                    53,
                    54
                ],
                "updated_at": "2000-01-01 00:00:00",
                "username": "unknow",
                "uuid": "5fffffb0-ecf0-6623-97f2-00ebe4d84212"
            }
        ]
    }
}
``` 