# 数据中心拓扑 API 文档（power.json）

## 基本信息
- 接口地址：`http://api.joypaw.jd.com/topology/power/get-data`
- 请求方法：POST
- 请求体类型：`application/json`
- 功能：按数据中心与模组返回电力拓扑（结构同 `power.json`），可选择是否带故障信息。

## 请求体示例
```json
{
  "idc": 3,        // 数据中心 ID
  "module": 68,    // 模组 ID
  "is_simulate": 0,
  "fault": true    // 是否查询故障，默认不查
}
```

## 返回结构
- 参考文件：[`power.json`](power.json)
- 核心包含：substation / transformer / ups / building / room / rpp / row / cabinet 及其 link/line 关系、fault_list。

## 建模流程建议（端到端）
1) 查询数据中心列表（[`数据中API.md`](数据中API.md)），获取 dc_id / dc_name / address 等。
2) 查询指定数据中心的模组（[`数据中心模组API.md`](数据中心模组API.md)），获取 module_id / module_name / building 关联。
3) 调用拓扑 API（本接口）按 dc_id + module_id 拉取电力拓扑（power.json 结构）。
4) 查询机柜列表（[`机柜API.md`](机柜API.md)）按 idc/room/building 过滤，获取机柜容量、供电参数、U 位占用。
5) 查询机柜内设备（[`设备查询接口API.md`](设备查询接口API.md)）按 cabinet_id/room_id 等过滤，拉取服务器/网络设备，写入设备节点及机柜内位置。
6) 汇总：以 Substation→HighLineCabinet→Transformer→UPS→RPP→Row→Cabinet→Device 的链路，叠加空间层级 DataCenter→Module→Building→Room→Row→Cabinet，完成电力与放置关系的双视图。

## 字段映射与关系提示
- 输入参数：`idc`→DataCenter.dc_id，`module`→Module.module_id。
- 返回数据：同 `power.json` 中的节点与关系，可直接映射 Neo4j/图模型；fault_list 可作为节点属性。
- 结合其他接口：机柜与设备需通过 cabinet/room/building/idc 关联到拓扑中的空间层级。

## Go 结构体（请求/响应简版）
```go
type PowerTopoRequest struct {
    IDC        int  `json:"idc"`
    Module     int  `json:"module"`
    IsSimulate int  `json:"is_simulate"`
    Fault      bool `json:"fault"`
}

// 响应结构可复用 power.json 的节点/关系定义，按需建模。
```

## 调用提示
- 若只需正式数据，`is_simulate`=0；如需演练可设为 1（视平台支持）。
- `fault=true` 才会返回 fault_list，需时再打开以减小返回体。
- 请先获取 dc_id 与 module_id，再调用本接口；若需全量遍历，可循环数据中心与模组组合。