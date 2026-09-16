
## 获取计算面全部POD列表（计算面表示，GPU集群）
请求地址：http://cmdb.jd.com/pub/api/v3/find/instance/object/pod?user=cmdb_all&timestamp=1&auth=1

请求方式: POST
其他描述： plane=2 表示计算面就是GPU集群涉及的设备
```json
{
    "fields":[
    
    ],
    "page": {
        "start": 0,
        "limit": 10,
        "sort": "inst_id"
    },
    "condition":{
         "plane":2, //平面类型 管理面(1, 默认值) 计算面(2) 存储面(3) 带外管理面(4)
    }

}

```

返回值
```json
{
    "result": true,
    "error_code": 0,
    "error_msg": "success",
    "permission": null,
    "data": {
        "count": 58,
        "info": [
            {
                "action_id": "977279425206415360",
                "basic_code": "SQV05-POD011",
                "created_at": "2022-04-24 15:31:59",
                "creator": "",
                "deleted_at": "2000-01-01 00:00:00",
                "describe": "建设",
                "full_name": "宿迁湖滨新区T2-POD011",
                "id": 944,
                "idc_logic_id": 28,
                "inst_id": 944,
                "is_delete": 0,
                "is_sync": 0,
                "lables": [
                    2
                ],
                "last_time": "2025-10-16T19:40:37.054+08:00",
                "mode": "L3",
                "modifier": "fangmeng3",
                "name": "POD011",
                "obj_id": "pod",
                "phy_building_id": 176,
                "plane": 2,
                "rdma": 2,
                "supplier_account": "0",
                "support_ids": [
                    53
                ],
                "support_service_ids": [
                    53,
                    50
                ],
                "updated_at": "2025-05-20 18:39:32",
                "username": "unknow",
                "uuid": "89ecd274-9539-ee72-7a12-ad380db98886"
            }
        ]
    }
}
```

## 获取计算面全部POD列表（计算面表示，GPU集群）
