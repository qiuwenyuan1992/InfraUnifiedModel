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
  "uuid": "9d159857-ab43-428b-b384-81e77e9f2539",
  "inst_id": 1,
  "cn_name": "廊坊_中国联通_磐石",
  "code": "PS",
  "address": "河北省廊坊市开发区娄庄路100号中国联通华北基地",
  "alias": "廊坊联通磐石机房",
  "device_num": 56369,
  "network_device_num": 6608,
  "cabinet_num": 6715,
  "cabinet_power_num": 6000,
  "server_num": 49698,
  "latitude": 39.6,
  "longitude": 116.73,
  "location": "磐石",
  "city_id": 761,
  "zone_id": 597,
  "idc_service_level": "A",
  "idc_ops_status": 2,
  "attribute": 1,
  "type": 4,
  "on_duty_phone": "13292680302",
  "principal_name": "王浩",
  "principal_erp": "wanghao164",
  "principal_email": "wanghao21@jd.com",
  "principal_tel": "13292680302",
  "asset_user_name": "张翠翠",
  "asset_user_phone": "17731650330",
  "asset_user_email": "idc-panshi@jd.com",
  "outsources_name": "金石",
  "outsources_tel": "13292680302",
  "outsources_email": "idc-panshi@jd.com",
  "support_email": "fc3@cnispgroup.com,idc-panshi@jd.com,hq-CloudLFMC@chinaunicom.cn",
  "action_id": "20250730180310",
  "created_at": "2018-03-07 08:13:55",
  "updated_at": "2026-01-20 23:01:19",
  "username": "cmdb_sys:statisticTask",
  "jira_key": "NW-734756"
}
```
第二条示例（广州南沙）字段类似，`cn_name` 为 “广州_中国电信_南沙”，`code` 为 “NS”，含地址、经纬度、负责人等。

## 字段优先级（建模必选 vs 可选）
- 建模必选（建议入图/入库）：
  - 标识：uuid, inst_id/id, code, cn_name/alias
  - 位置：address, latitude, longitude, city_id, zone_id
  - 规模：cabinet_num, device_num, server_num, network_device_num
  - 状态：idc_ops_status, idc_service_level, status, is_delete
  - 联系：principal_name, principal_tel（或 on_duty_phone）
- 重要可选（有则存储，便于运维/可视化）：
  - cabinet_power_num, external_num, other_num
  - location（片区简称）, geographic_location_id
  - principal_email/erp，asset_user_*，outsources_*，support_email
  - rental_mode, attribute, type, management_department, manager_type, monitor
  - open_at/close_at/created_at/updated_at/last_time
- 低优先级（可不建模，需时再拉取）：
  - support_*_ids（需结合业务时才用）
  - is_core_4_gpu, logic_idc_id, net_zone, username, jira_key, action_id, mounted_node_id

## 字段含义与拓扑模型映射（DataCenter 节点）
- 标识类：
  - `uuid` → DataCenter.uuid
  - `inst_id`/`id` → DataCenter.dc_id
  - `code` → DataCenter.code（短名）
  - `cn_name`/`alias` → DataCenter.dc_name / 别名
- 位置类：
  - `address` → DataCenter.address
  - `location`（如“磐石”“南沙”）→ 可作简址或片区
  - `latitude`/`longitude` → 经纬度
  - `city_id`/`zone_id`/`geographic_location_id` → 城市/大区/地理域
- 规模与容量：
  - `cabinet_num`/`cabinet_power_num` → 机柜数/供电位
  - `device_num` → 设备总数
  - `server_num` → 服务器数量
  - `network_device_num` → 网络设备数量
  - `external_num`/`other_num` → 其他资源数
- 运营状态：
  - `idc_ops_status` → 运维状态
  - `idc_service_level` → 等级（如 A）
  - `open_at`/`close_at`/`last_time`/`updated_at` → 时间类
  - `status`/`is_delete` → 启用/删除标记
- 管理与责任人：
  - `principal_*` → 负责人信息
  - `asset_user_*`/`asset_erp` → 资产 Owner
  - `outsources_*`/`support_email`/`support_ids` → 供应商/支持链路
  - `on_duty_phone` → 值班电话
- 业务属性：
  - `attribute`/`type`/`rental_mode` → 自建/托管/租赁等
  - `support_application_ids`/`support_service_ids`/`support_business_model_ids` → 支撑应用/服务/业务模型
- 其他：
  - `management_department`/`manager_type`/`monitor` → 管理与监控标识
  - `is_core_4_gpu` → 是否核心 GPU
  - `logic_idc_id` → 逻辑 IDC 关联（若有）

## 请求与分页说明
- 认证：通过 URL query（user/timestamp/auth），若有签名机制需按平台要求补全。
- 分页：`page.start`、`page.limit` 控制分页，`page.sort` 指定排序。
- 过滤：若需按名称/编码/城市等过滤，可在请求体增加平台支持的过滤字段（未在示例中展示）。

## Go 结构体示例（关键字段精简版）
```go
type IDCRequest struct {
    Fields []string `json:"fields"`
    Page   struct {
        Start int    `json:"start"`
        Limit int    `json:"limit"`
        Sort  string `json:"sort"`
    } `json:"page"`
}

type IDCResponse struct {
    Code int    `json:"code"`
    Msg  string `json:"msg"`
    Data []IDC  `json:"data"`
    Page struct {
        Start int `json:"start"`
        Limit int `json:"limit"`
        Total int `json:"total"`
    } `json:"page"`
}

type IDC struct {
    UUID             string  `json:"uuid"`
    InstID           int     `json:"inst_id"`
    ID               int     `json:"id"`          // 若返回
    Code             string  `json:"code"`
    CNName           string  `json:"cn_name"`
    Alias            string  `json:"alias"`
    Address          string  `json:"address"`
    Latitude         float64 `json:"latitude"`
    Longitude        float64 `json:"longitude"`
    CityID           int     `json:"city_id"`
    ZoneID           int     `json:"zone_id"`
    CabinetNum       int     `json:"cabinet_num"`
    DeviceNum        int     `json:"device_num"`
    ServerNum        int     `json:"server_num"`
    NetworkDeviceNum int     `json:"network_device_num"`
    IDCServiceLevel  string  `json:"idc_service_level"`
    IDCOpsStatus     int     `json:"idc_ops_status"`
    Status           int     `json:"status"`
    IsDelete         int     `json:"is_delete"`
    PrincipalName    string  `json:"principal_name"`
    PrincipalTel     string  `json:"principal_tel"` // 或 on_duty_phone
}
```

## 与拓扑建模的衔接
- DataCenter 节点建议字段：dc_name(cn_name/alias)、code、dc_id(inst_id/id)、uuid、address、lat/lng、city/zone、规模（cabinet/device/server/network_device）、等级/状态、负责人/供应商联系信息。
- 后续可将 DataCenter 与 Module/Building/Room 的 CONTAIN 关系挂接；地址用于空间落点，经纬度可支撑可视化。

## 调用提示
- 若需更多字段，在请求体 `fields` 中追加；未声明的字段将不返回。
- 如果 API 默认返回全字段，可保留现状；如需减流量，显式列举需要的字段。
- 建议对 `limit` 设置合理值（如 100/500）并循环翻页获取全量。