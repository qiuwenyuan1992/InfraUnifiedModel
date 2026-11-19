# dci 动环域告警事件结构解析（基于 20 条真实事件，2026-11-19）

> 样本：机柜 PDU 加和电流超技术峰值（张家口×4）、PDU 单路掉电（昌金路）、机柜列超电模板（秦皇岛×12）、改电机柜超业务峰值（中云信/昌金路）。
> 来源：告警列表接口（total_elements=160,646，8033 页——**动环告警量三域最大**，持续告警聚合为主）。

## 一、事件结构（三域最简形态）

```
告警记录（平铺，无嵌套）
├── 规则：event_rule_name/id（=策略名）、application（dcim | cabinet_row）
├── 等级：event_level_name（L1-L4）
├── 定位五级：idc → building → ops_it_room → idc_module → cabinet_code
├── 对象：ops_device_id/name（机柜码，如 2A-207-F-04）
├── ⭐监控点路径：idc_monitor_lable_ops_module_name
│   "电力设施--列头柜的支路开关--机柜PDU-A2电流IA2"
│   ——层级化路径：对象族--设备--部件--测点（含相别 A/B/C + 回路号）
├── 数值：alarm_value（112.40A / 0.00A）
├── 聚合：alarm_count（同告警累计次数，最高 2290）、analysis_time↔last_analysis_time
├── 处置：op_status（0/1）+ operator（ext.idc.* 机房值守账号）+ comment（"已在京ME端处理"）
└── 去重键：unique_message = rule_id:ops_device_id（如 1852:107002）
```

## 二、关键发现

### 2.1 监控点路径 = 动环对象树词汇（金矿）
`电力设施--列头柜的支路开关--机柜PDU-A2电流IA2`——三段式：**对象族（电力设施）--设备（列头柜支路开关）--测点（PDU-A2 相电流 IA2）**。
- 测点词汇：PDU 相电流（IA1/IA2/IB1/IB2…——A/B 相+回路号）→ 字典编码 `power.rack.pdu_current.over` 的实例属性直接可抽
- 这就是 DCIM 侧的**监控点命名体系**（策略无表达式的谜底：判定在 DCIM 按测点做，dci 只收结果）
- 字典的"对象.现象"结构与此路径天然同构——摄取时监控点路径可作对象定位中间层

### 2.2 application 分族：dcim vs cabinet_row（新对象：机柜列）
- `dcim`（机柜级）：PDU 掉电/加和电流超峰
- `cabinet_row`（**机柜列**，集合对象）：机柜列超电模板——ops_device_name="3-1-A26-A50"（**列范围而非单柜**），alarm_value 300-500A 级
- **对象层级三级**：机柜列（cabinet_row）→ 机柜（cabinet）→ PDU/测点——容量类故障的三层放大（列超电 L2 > 柜超技术峰 L3 > 柜超业务峰 L4，层级越高等级越高）

### 2.3 L 等级实例与三档容量语义
- L2=机柜列超电（列级跳闸风险）、L3=机柜超技术峰值（供电设计上限）、L4=机柜超业务峰值（业务申报值）——**两套阈值体系实证**（技术峰值 vs 业务峰值，同机柜可同时触发两档）
- PDU 单路掉电 L3 实证（策略池判定 L3 吻合）

### 2.4 处置形态：京ME 端 + 机房值守账号
- op_status=1 + operator（ext.idc.机房名1，如 ext.idc.wanguo1/ext.idc.zhongyunxi1——**按机房命名的值守账号**）+ comment（"已在京ME端处理"）
- 与网络域工单流（workStatus/callbackResult）对照：动环处置在**外部系统（京ME）**完成，dci 只留结果标记——摄取时处置过程数据需另接京ME

### 2.5 持续告警聚合特征
alarm_count 高达 2290（秦皇岛列超电）——动环容量类告警长期不恢复（业务增长型超电，非故障态而是**容量债务**）；recover_time=null。→ 字典消费策略：容量类（over 系列）与故障类（outage/掉电）要分开对待——前者是渐进债务、后者是突发事件。

### 2.6 PDU 单路掉电的观测机制
PDU-B1 电流 IB1=**0.00A** 判定（非开关量，是**电流归零**）——掉电类靠测点电流值，非 trap。

## 三、三域事件结构总对照

| 维度 | cloudbee 服务器 | netops 网络 | dci 动环 |
|---|---|---|---|
| 结构 | 告警实例+etl_data | 故障单+eventCollection+work | **平铺列表**（无嵌套） |
| 定位 | cmdb_extend（本机） | ip:port / GUID对 / 告警名+位置 | **五级机房层级+机柜码+监控点路径** |
| 对象 | 部件（GPU/盘…） | 端口/链路/设备 | 机柜/机柜列/PDU 测点 |
| 处置 | is_push_repair 标志 | 工单+操作流水内嵌 | 京ME 外部系统+op_status 结果 |
| 聚合 | alert_time/latest | eventCollection | alarm_count 计数 |
| 等级 | S→P0-P3 | A→P0-P4 | L→P0-P3 |

## 四、对字典成稿的输入

1. 编码对象层级按"机柜列/机柜/PDU 测点"三级设计（容量类三档已实证）
2. 监控点路径词汇进观测面（IA1/IB2 相电流命名体系）
3. 容量类（over 族）与故障类（outage/掉电族）分开标注消费策略
4. application 字段（dcim/cabinet_row）是对象族判别键——映射键之一

## 五、已恢复形态与生命周期（2026-11-19 补：CDU 漏液 + 上海 UPS 电池电压低样本）

- **alert_type 判别**：1=活动中（前 20 条，recover_time=null）/ **2=已恢复**（两新样本，recover_time 非空）——恢复判定在动环域原生存在（对照：网络域靠 5 次查询验证、服务器域靠恢复词）
- **完整生命周期三时间戳**：analysis_time（首发）→ last_analysis_time（末次）→ recover_time（恢复）：CDU 漏液 24 分钟恢复、电池电压低 8 分钟恢复
- **op_status 新值 4**（电池样本：无 operator/comment）——疑似恢复自动关单（op_status=1 人工处理、4 自动？待更多样本）
- **监控点路径第二/三族**：`暖通系统--液冷CDU--漏水告警--FO`（液冷属**暖通系统**族非电力设施）、`电力设施--UPS系统--电池总电压--BatVol`（UPS 系统级）——监控点对象族词汇：电力设施/暖通系统两大族
- **alarm_value 类型学**：开关量（漏水 1.00）/ 模拟量（电压 236.30V）/ 模拟量（电流 112.40A）——映射时按监控点测点类型区分
- **⚠️ 等级与启停两处修正**：①上海 UPS 电池电压低样本实测 **L3**（策略池记 L2——等级修正样本优先）；②上海族策略 2026-10 仍在产告警——"上海族整体下线"存疑，至少部分存活（策略池 status 读取时的停用集合需复核）

## 六、待办

1. ~~UPS/市电/CDU 液冷类告警样本~~ 已到 2 条（CDU 漏液+UPS 电池），市电/变压器类仍缺（不阻塞）
2. 上海族策略启停状态复核（策略池 status 与实际产告警矛盾）
