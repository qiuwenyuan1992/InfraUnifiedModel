# 数据中心房间API

来源证据：`../../../UnifiedInfrastructureModel/evidence/数据中心房间API.md` 与同目录JSON，由用户提供。认证参数沿用来源配置，不在此复制凭据。

接口：POST `/pub/api/v3/find/instance/object/room`。

```json
{"fields":[],"page":{"start":0,"limit":1000,"sort":"inst_id"},"condition":{"idc_id":1030,"status":1}}
```

返回 `data.count` 为总数，`data.info` 为当前页；示例总数32但仅两条记录，不得视为完整目录。

|字段|语义|
|---|---|
|uuid|房间稳定身份|
|inst_id|正整数，供机柜/RPP引用解析|
|idc_id|数据中心inst_id|
|room_full_name|展示全名|
|name / full_name / isp_room_name|来源名称属性|
|phy_building_id / building_full_name|楼栋引用，暂不独立建模|
|idc_module_id|模组引用，暂不独立建模|
|status / ops_status / room_type|来源状态与类型|

样例：房间3094，UUID `9e2be1c5-ffe8-bbc6-6d61-d0a9b2ce5ee2`，IDC1030，名称 `中卫_中国电信_算力网络_A4_101`；另一房间3095名称A4_102。

关系：room.idc_id→data_center.inst_id；cabinet.phy_room_id→room.inst_id；idc_RPP.idc_room→room.inst_id。引用仅用来解析目标UUID，不能用数字房间ID替代稳定身份。缺失/歧义引用诊断跳过，不凭同房间推导机柜→RPP供电，也不凭编码补A/B供电。

原证据复杂查询字段tree_idc_id换行损坏，本次只采用已确认的简单condition请求；不猜测接口兼容形态。
