# 数据中心信息查询 API 文档

## 基本信息
- 接口地址：`http://cmdb.jd.com/pub/api/v3/find/instance/object/idc?user=cmdb_all&timestamp=1&auth=1`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：查询数据中心（IDC）的基础信息列表（支持分页与字段选择）。

## 请求体字段
```json
{
  "fields": [
    "uuid",
    "inst_id",
    "cn_name",
    "code",
    "device_num",
    "network_device_num"
  ],
  "page": {
    "start": 0,
    "limit": 2,
    "sort": "inst_id"
  },
    "condition":{
        "inst_id":451
    }
}
```
- `fields`：数组，指定需要返回的字段。
  - `uuid`：数据中心唯一标识。
  - `inst_id`：实例 ID（dc_id）。
  - `cn_name`：中文名称（数据中心名称）。
  - `code`：编码（短名）。
  - `device_num`：服务器/设备数量。
  - `network_device_num`：网络设备数量。
- `page.start`：起始偏移（从 0 开始）。
- `page.limit`：分页大小。
- `page.sort`：排序字段（例：`inst_id`）。

## 实际返回示例（用户提供）
接口实测返回包含大量字段（下方节选两条）：
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
                "FM_email": "",
                "FM_erp": "",
                "FM_name": "",
                "FM_phone": "",
                "IT_manage_department": 1,
                "action_id": "20250730180310",
                "address": "河北省廊坊市广阳区经济技术开发区润惠道66号",
                "alias": "京东集团华北（廊坊）云数据中心,京东云华北数据中心",
                "asset_erp": "ext.yangyanan5",
                "asset_user_email": "idc-runhui@jd.com",
                "asset_user_name": "杨亚楠",
                "asset_user_phone": "15705003226",
                "attribute": 0,
                "cabinet_num": 6623,
                "cabinet_power_num": 6340,
                "city_id": 761,
                "class_ids": [
                    16,
                    18
                ],
                "close_at": "2000-01-01 00:00:00",
                "cn_name": "廊坊_京东_润惠",
                "code": "RH",
                "created_at": "2021-03-15 18:02:11",
                "deleted_at": "2000-01-01 00:00:00",
                "device_num": 51231,
                "en_name": "",
                "external_num": 325,
                "geographic_location_id": 1,
                "group": 2,
                "id": 451,
                "idc_onsite_number": 11,
                "idc_ops_status": 2,
                "idc_service_level": "A",
                "idc_teamleader_erp": "ext.mayan70",
                "inst_id": 451,
                "is_core_4_gpu": 1,
                "is_delete": 0,
                "is_it_oms": 1,
                "is_sync": 0,
                "it_service_time": 3,
                "jira_delete": 0,
                "jira_key": "NW-1319263",
                "last_time": "2026-09-15T23:01:33.343+08:00",
                "latitude": 0,
                "location": "润惠",
                "logic_idc_id": null,
                "longitude": 0,
                "management_department": 8,
                "manager_type": 1,
                "modifier": "fanyuhui5",
                "monitor": 0,
                "mounted_node_id": 1045020,
                "net_zone": "",
                "network_device_num": 5327,
                "obj_id": "idc",
                "on_duty_phone": "17503164208",
                "open_at": "2000-01-01 00:00:00",
                "other_num": 712,
                "outsources_email": "idc-runhui@jd.com",
                "outsources_erp": "ext.idc.runhui1",
                "outsources_name": "金石",
                "outsources_tel": "17503164208",
                "principal_email": "wanghao21@jd.com",
                "principal_erp": "wanghao164",
                "principal_name": "王浩",
                "principal_tel": "13292680302",
                "rental_mode": 1,
                "server_manage_department": 1,
                "server_num": 45192,
                "status": 1,
                "supplier_account": "0",
                "support_application_ids": [],
                "support_business_model_ids": [
                    991
                ],
                "support_email": "muchao5@jd.com,sunyue97@jd.com,bfliyu3@jd.com,guoyutong@jd.com,liuer@exceam.com,2100974724@qq.com",
                "support_ids": [
                    26,
                    53,
                    991,
                    983
                ],
                "support_isp1_ids": [],
                "support_isp2_ids": [
                    26
                ],
                "support_node_role_ids": [
                    983
                ],
                "support_service_ids": [
                    53
                ],
                "type": 0,
                "updated_at": "2026-09-15 23:01:33",
                "updated_time": "2026-09-15T23:01:33.343+08:00",
                "username": "cmdb_sys:statisticTask",
                "uuid": "0e32c4d6-a29f-4d8b-94c8-38ac791e363d",
                "zone_id": 597
            }
        ]
    }
}
```