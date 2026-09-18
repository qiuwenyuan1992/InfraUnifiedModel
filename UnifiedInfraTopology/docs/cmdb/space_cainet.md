
作用：查询出机柜信息
请求地址： http://cmdb.jd.com/pub/api/v3/find/instance/object/cabinet?user=cmdb_all&timestamp=1&auth=1
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
   "condition": {
        "idc_id": 1,
        "idc_module_id": 58,
        "phy_room_id": 213,
        "is_delete": 0
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
                "10a_outlets": 40,
                "16a_outlets": 8,
                "PDU_Phase": 0,
                "action_id": "20241030191940",
                "allow_use_current": 22,
                "app_id": 0,
                "application": 0,
                "apply_uuid": "",
                "assigned_time": "2022-04-26 00:00:00",
                "basic_code": "",
                "bmc_type_id": 0,
                "borrow_time": "2022-03-04 00:00:00",
                "building_full_name": 404,
                "business_model": 991,
                "cabinet_lable_server": 136,
                "code": "M1-F2-IT06-R08-16",
                "column_air_conditioner": "",
                "company": 294,
                "contract": "",
                "cost": "4600.00",
                "cost_currency_id": 1219,
                "created_at": "2021-12-23 16:06:45",
                "current_free_period": 0,
                "customer": "",
                "dc_colo_rack": "LFG21_F2-IT06_R08-16",
                "deleted_at": "2000-01-01 00:00:00",
                "delivered": "true",
                "device_num": 6,
                "disabled_u": "",
                "empty_cabinet": "",
                "expired_at": "2000-01-01 00:00:00",
                "external_num": 0,
                "full_rack_type": 2,
                "gpu_device_num": 0,
                "id": 62897,
                "idc_building_structure_id": 1728,
                "idc_id": 451,
                "idc_module_id": 100,
                "idc_rows_code": 15756,
                "inst_id": 62897,
                "is_delete": 0,
                "is_manual": 0,
                "is_odf": 2,
                "is_sync": 0,
                "isp_rack_code": "M1-F2-IT06-R08-16",
                "isp_rated_current": 32,
                "jira_delete": 0,
                "jira_key": "NW-1329064",
                "last_time": "2026-09-17T06:19:20.878+08:00",
                "level": 3,
                "lock": "false",
                "max_current": 64,
                "maximum_current": 64,
                "modifier": "cmdb_all",
                "net_rate": "",
                "network_device_num": 1,
                "obj_id": "cabinet",
                "occupy_type_lv1": 2,
                "occupy_type_lv2": 2,
                "opening_at": "2000-01-01 00:00:00",
                "ops_status": 11,
                "order_issue": "NWRESOURCE-10437",
                "organization_codes": [
                    "00095217",
                    "00140009"
                ],
                "organization_codes_lv1": [
                    "00013807",
                    "00008987"
                ],
                "organization_codes_lv2": [
                    "00024935",
                    "00017690"
                ],
                "other_num": 0,
                "outlets_10a": 40,
                "outlets_10a_single": "20",
                "outlets_16a": 8,
                "outlets_16a_single": "4",
                "pdu_num": 0,
                "pdu_plug_type": [
                    3
                ],
                "phy_building_id": 404,
                "phy_idc_id": 451,
                "phy_room_id": 889,
                "pod_id": 0,
                "pod_ids": [
                    914
                ],
                "power_lock": 0,
                "power_state": 2,
                "power_status": 1,
                "power_type": 2,
                "procurement_contract_key": "",
                "procurement_contract_val": "",
                "rack": "_R08-16",
                "rack_column_code": "M1-F2-IT06 R08",
                "rack_row": "",
                "rack_size": 9,
                "rack_tor_fix": "M1-F2-IT06-R08-14,M1-F2-IT06-R08-15",
                "rack_tor_inter_speed": "25Gbps",
                "rack_tor_type": "二拖四",
                "rated_current": 20,
                "released_time": "2022-04-22 00:00:00",
                "remain_power": 243,
                "remould": "",
                "rent_time": "2022-04-26 00:00:00",
                "rest_a_pdu": 0,
                "rest_b_pdu": 0,
                "return_time": "2022-03-13 00:00:00",
                "rfid_reader": "",
                "room_full_name": 889,
                "row_switch_id_A": 6204,
                "row_switch_id_B": 6114,
                "server_num": 5,
                "service": 50,
                "spare_u_num": 32,
                "status": "Assigned",
                "supplier_account": "0",
                "support_application_ids": [],
                "support_business_model_ids": [
                    991
                ],
                "support_company_ids": [],
                "support_cost_currency_ids": [],
                "support_customer_ids": [],
                "support_ids": [
                    991,
                    134,
                    135,
                    50
                ],
                "support_in_jira": "",
                "support_outlets_type_ids": [
                    134,
                    135
                ],
                "support_service_ids": [
                    50
                ],
                "surrender_time": "2022-04-22 00:00:00",
                "switch_peak_power": 0,
                "target_rent_time": "2022-04-26 00:00:00",
                "target_surrender_time": "2022-04-22 00:00:00",
                "tec_max_current": 24,
                "tem_power_day": 0,
                "temp_electrified": 0,
                "u_num": 46,
                "unit_use_rule": 4,
                "updated_at": "2026-09-17 06:19:20",
                "updated_time": "2026-09-17T06:19:20.878+08:00",
                "usable_1u": 0,
                "usable_2u": 0,
                "usable_4u": 0,
                "usage_type": 1,
                "use_level": 1,
                "used_rated_power": 4357,
                "used_u_num": 14,
                "username": "cmdb_sys:task-remainingPower",
                "utilization_ratio": 0.8477,
                "uuid": "c0b7b4f5-c5b2-ab3d-af4e-da3fbe7c055b",
                "width": 0
            }
        ]
    }
}
```

机柜信息不需要关注太多了。只要关注以下字段就行
"code": "DC2-M1-219-A01",//机柜编码
"dc_colo_rack": "LFG11_M1-219_A01",//建筑_房间_机架
"isp_rack_code": "DC2M1-219 A01",//运营商机柜编码
"device_num": 11,
"inst_id": 3661,//机柜的id
"idc_building_structure_id": 4,
"idc_id": 1,
"idc_module_id": 58,
"idc_rows_code": 12547,//机柜列编码
"jira_key": "NW-726467",//jira库键值
"rack": "_A01",
"rack_column_code": "DC2M1-219 A",
"rack_row": "",
"rack_size": 8,
"rack_tor_fix": "DC2-M1-219-A01",//tor所在机柜
"rack_tor_inter_speed": "10Gbps",  //tor端口运行速率
"room_full_name": 213,
"row_switch_id_A": 4193, //连接的列头柜 id
"row_switch_id_B": 4150, //连接的列头柜 id
"server_num": 9,
"service": 1336,
"spare_u_num": 31,

完整的描述：
字段名	字段名	类型
error_msg	接口返回结果描述	string
error_code	接口返回状态（0:正常）	int
data.count	接口返回总条数	int
data.info.inst_id	机柜ID	int
data.info.code	机柜编码	string
data.info.idc_id	机房id	int
data.info.phy_building_id	楼宇id	int
data.info.phy_room_id	房间Id	int
data.info.uuid	uuid	string
data.info.rack	机架	string
data.info.u_num	机柜高度（机柜总U数）	int
data.info.power_status	通电状态（0:未通电 1:通电）	int
data.info.status	机柜状态（Assigned：已分配、Dissolved：裁撤、Faulty：不可用、Free：空闲、Ordered：预留）	string
data.info.device_num	机柜上设备数量	int
data.info.cabinet_num	机柜上服务器数量	int
data.info.network_device_num	网络设备数量	int
data.info.other_num	其他设备数量	int
data.info.external_num	第三方设备数量	int
data.info.spare_u_num	剩余U位	int
data.info.used_u_num	已用U位	int
data.info.rated_current	合同电流	float
data.info.usage_type	机柜类型（0:其他 1:服务器机柜 2:网络机柜）	int
data.info.outlets_10a	10A插座数量	int
data.info.outlets_16a	16A插座数量	int
data.info.pdu_num	PDU数量	int
data.info.info.rest_a_pdu	剩余A路PDU数量	int
data.info.rest_b_pdu	剩余B路PDU数量	int
data.info.opening_at	开通日期	string
data.info.expired_at	失效日期	string
data.info.maximum_current	空开电流	int
data.info.jira_key	JIRA键值（OPJIRA中的唯一值）	int
data.info.company	供应商	int
data.info.dc_colo_rack	建筑_房间_机架	string
data.info.remould	电力改造	string
data.info.order_issue	预定提案(OPJIRA)	string
data.info.target_surrender_time	目标退租时间	string
data.info.target_rent_time	目标起租时间	string
data.info.rent_time	启用时间	string
data.info.surrender_time	停用时间	string
data.info.temp_electrified	服务等级（A，B，C，D）	string
data.info.temp_electrified	临时加电状态（0:未加电 1:加电）	int
data.info.borrow_time	临时加电开始时间	string
data.info.return_time	临时加电结束时间	string
data.info.assigned_time	分配时间	string
data.info.released_time	释放时间	string
data.info.max_current		int
data.info.ops_status	运维状态（1:正常 11:预占锁定 12:计划裁撤 13:裁撤中 14:已裁撤 9:建设中 15:被借电 16:不可用)	int
data.info.idc_module_id	模组id	int
data.info.room_full_name	房间id	int
data.info.idc_rows_code	机柜列编码id	int
data.info.isp_rack_code	运营商机柜编码	string
data.info.power_state	运维-开电状态(1:未开电 2:开电 3:被借电）	int
data.info.borrow_rack	借电机柜id	int
data.info.isp_rated_current	机柜空开额定电流	string
data.info.row_switch_id_A	列头柜空开A路id	int
data.info.row_switch_id_B	列头柜空开B路id	int
data.info.power_on	运维-开电时间	string
data.info.power_off	运维-关电时间	string
data.info.is_temporary_power	是否有临电（0:否 1:是）	int
data.info.tem_power_day	允许临电天数	int
data.info.tem_power_on	运维-临时开电时间	string
data.info.tem_power_off	运维-临电关电时间	string
data.info.allow_use_current	业务峰值	int
data.info.tec_max_current	技术峰值	int
data.info.power_type	机柜电源类型（1:直流+直流 2:交流+交流 3:直流+交流）	int
data.info.pdu_plug_type	PDU插口标准（1:欧标IEC 2:英标BS 3:国标GB）	int[]
data.info.outlets_10a_single	10A插口数量(单路)	string
data.info.outlets_16a_single	16A插口数量(单路)	string
data.info.full_rack_type	是否整机柜（1:是 2:否）	int
data.info.is_odf	是否ODF机柜（1:是 2:否）	int
data.info.unit_use_rule	U位使用规则（1:紧密部署 2:4U空1U 3:2U空1U）	int
data.info.empty_u_code	需留空U位号	string
data.info.pillar	综布规划-柱子位置	string
data.info.idc_logic_id	综布规划-逻辑机房	int
data.info.pod_id	综布规划-POD id	int
data.info.cabinet_lable_server	业务标签id	int
data.info.rack_tor_inter_speed	TOR端口运行速率	string
data.info.rack_tor_fix	TOR所在机柜	string
data.info.rack_tor_type	TOR架构	string
data.info.power_lock	超电锁定（0:否 1:是）	int
data.info.support_service_ids	服务id（业务线）	int[]
data.info.support_business_model_ids	商务模式id	int[]
data.info.support_outlets_type_ids	插座类型id	int[]
data.info.support_application_ids	应用id（用途）	int[]
data.info.support_customer_ids	客户id	int[]