
作用：查询出房间。可以和数据中心绑定。楼宇绑定。下一层级还有房间
请求地址： http://cmdb.jd.com/pub/api/v3/find/instance/object/room?user=cmdb_all&timestamp=1&auth=1
请求方式：post



请求参数：
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
        "status":1
    }

}
```
复杂查询
```json
{
    "fields":
    [],
    "page":
    {
        "start": 0,
        "limit": 10,
        "sort": "-inst_id"
    },
    "conditions":
    {
        "condition": "AND",
        "rules":
        [
            {
                "field": "tree_idc_id",
                "value":
                [],
                "operator": "in"
            },
            {
                "field": "status",
                "value":
                [
                    1
                ],
                "operator": "in"
            }
        ]
    }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition` 可选，是用来控制查询的，
- `condition.idc_id`：按数据中心 ID 查询。房间
- `condition.idc_module_id`：模组id 查询。房间
- `condition.status`： 查询可用的楼宇
- `conditions` 可选组合复杂查询

[返回数据](数据中心房间API.json)