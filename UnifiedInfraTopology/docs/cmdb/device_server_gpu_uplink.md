# 服务器的GPU卡对应的上联关系 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/gpu_uplink?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：服务器的GPU卡列表（可分页、可选字段）。

## 请求体示例
```json
{
    "fields": [],
    "page": {
        "start": 0,
        "limit": 11,
        "sort": "-inst_id"
    },
    "condition": {
        "device_sn": "219685020073",
        // "gpu_sn": "MY15960263P00569"
    }
}
```
- `fields`：可选，列出需要返回的字段；不指定时可返回全字段。
- `page.start` / `page.limit` / `page.sort`：分页与排序。
- `condition.device_sn`：按设备SN过滤。
- `condition.gpu_sn`：配件SN过滤



## 返回字段补充

 - port_group_uuid  端口组uuid
 - group_id     端口组链路id，查询的是 port_group 接口，用 port_group_link_id = group_id 作为条件 可以查询出全部成员
 - group_type   端口组链路一级类型 (type:3,inner_link,机房内端口组) (type:1, dci:专线) (type:2，pop:出口) 
 - group_subtype  端口组链路子类型
 - group_uuid  端口组链路uuid 
 - port_group_id 本端端口组id ，是port_group 里面的其中一个成员的 inst_id 
 - port_group_uuid 本端端口组uuid 是port_group 里面的其中一个成员的 uuid 
 - port_group_name 本端端口组名称
 - port_group_type 端口组二级类型

## 返回数据样例
```json
{
    "result": true,
    "error_code": 0,
    "error_msg": "success",
    "permission": null,
    "data": {
        "count": 8,
        "info": [
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.457+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib8",
                "gpu_port_speed": "400G",
                "gpu_slot": "204",
                "gpu_sn": "MY15960263P01637",
                "gpu_vram": "80GB",
                "inst_id": 271585,
                "last_time": "2026-07-17T09:36:47.933+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "算力网络",
                "tor_ip": "127.0.0.1",
                "tor_name": "ZWU11_A4_201-C1-04_POD246_T0_CLD_016_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L100JC",
                "uuid": "24572728-bd42-4acb-bb94-d02552cf8600"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.448+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib7",
                "gpu_port_speed": "400G",
                "gpu_slot": "205",
                "gpu_sn": "MY15960263P00569",
                "gpu_vram": "80GB",
                "inst_id": 271584,
                "last_time": "2026-07-17T09:36:47.929+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "11.216.70.78",
                "tor_name": "ZWU11_A4_201-C1-03_POD246_T0_CLD_015_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L100HG",
                "uuid": "c4f2faec-5947-4885-a8d6-95960283cb67"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.441+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib6",
                "gpu_port_speed": "400G",
                "gpu_slot": "207",
                "gpu_sn": "MY15960263P01959",
                "gpu_vram": "80GB",
                "inst_id": 271583,
                "last_time": "2026-07-17T09:36:47.924+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.2",
                "tor_name": "ZWU11_A4_201-C1-02_POD246_T0_CLD_014_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L100GH",
                "uuid": "bf2fd028-fa18-4608-854d-c0a7ddd0919d"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.434+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib5",
                "gpu_port_speed": "400G",
                "gpu_slot": "206",
                "gpu_sn": "MY15960263P00580",
                "gpu_vram": "80GB",
                "inst_id": 271582,
                "last_time": "2026-07-17T09:36:47.92+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.3",
                "tor_name": "ZWU11_A4_201-C1-01_POD246_T0_CLD_013_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L100FG",
                "uuid": "f7505c8a-b9b8-44a0-8f42-a4258f54d81c"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.428+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib4",
                "gpu_port_speed": "400G",
                "gpu_slot": "200",
                "gpu_sn": "MY15960263P01942",
                "gpu_vram": "80GB",
                "inst_id": 271581,
                "last_time": "2026-07-17T09:36:47.916+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.4",
                "tor_name": "ZWU11_A4_201-B2-04_POD246_T0_CLD_012_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L10075",
                "uuid": "89077deb-39a3-4da6-a786-50cfbee29a8d"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.42+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib3",
                "gpu_port_speed": "400G",
                "gpu_slot": "201",
                "gpu_sn": "MY15960263P01938",
                "gpu_vram": "80GB",
                "inst_id": 271580,
                "last_time": "2026-07-17T09:36:47.911+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.6",
                "tor_name": "ZWU11_A4_201-B2-03_POD246_T0_CLD_011_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L1002Z",
                "uuid": "a7e4cd2c-65a6-4ce7-9b26-cd3878eb4d50"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.411+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib2",
                "gpu_port_speed": "400G",
                "gpu_slot": "203",
                "gpu_sn": "MY15960263P00575",
                "gpu_vram": "80GB",
                "inst_id": 271579,
                "last_time": "2026-07-17T09:36:47.907+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.9",
                "tor_name": "ZWU11_A4_201-B2-02_POD246_T0_CLD_010_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L10077",
                "uuid": "070c44ce-a796-4b29-b882-09be3190249f"
            },
            {
                "bond_name": "",
                "compute_building_code": [
                    "ZWU11"
                ],
                "compute_building_id": [
                    1036
                ],
                "compute_building_name": [
                    "中卫算力网络A4"
                ],
                "compute_pod_id": [
                    2289
                ],
                "compute_pod_name": [
                    "POD246"
                ],
                "create_time": "2026-07-09T22:00:23.403+08:00",
                "creator": "admin",
                "device_idc": "中卫_中国电信_算力网络",
                "device_ip": "11.217.112.89",
                "device_model": "R6900 G5",
                "device_sn": "219685020073",
                "gpu_ip": "",
                "gpu_model": "S5000 80GB",
                "gpu_port": "ib1",
                "gpu_port_speed": "400G",
                "gpu_slot": "202",
                "gpu_sn": "MY15960263P01966",
                "gpu_vram": "80GB",
                "inst_id": 271578,
                "last_time": "2026-07-17T09:36:47.902+08:00",
                "modifier": "admin",
                "obj_id": "gpu_uplink",
                "pod_id": 2289,
                "pod_name": "POD246",
                "server_port_speed": "100G",
                "source": "cloudbee",
                "supplier_account": "0",
                "tor_device_group": "2",
                "tor_idc": "中卫_中国电信_算力网络",
                "tor_ip": "127.0.0.11",
                "tor_name": "ZWU11_A4_201-B2-01_POD246_T0_CLD_009_GDR",
                "tor_port": "FourHundredGigE1/0/11",
                "tor_port_speed": "400000",
                "tor_role": "T0",
                "tor_sn": "210235A53L5264L1003T",
                "uuid": "ddaf6746-ed54-42ab-bdcc-bde97f288507"
            }
        ]
    }
}
```