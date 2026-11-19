# netops 网络域告警事件结构解析（基于两条真实事件：端口流量超限 + 端口 Down）

> 样本1：5分钟-业务线-OUT（Huawei CE6851 出口 40GE，OUT 利用率 73.16% > 70%，北京有孚B6）——traffic_port 指标族。
> 样本2：设备端口状态异常【T1以上】（H3C S9820-8C，PHY_UPDOWN HundredGigE down，廊坊）——syslog_port 族。
> 与服务器域（cloudbee）对比：结构**不同**且**丰富一整层**——网络域告警自带工单+操作流水+链路双端拓扑。

## 一、事件四层结构

```
故障单（faultId 顶层）
├── 元数据：name（策略名）/ level（A5）/ rootCause（traffic_port|syslog_port ←即category）
│          beginTime / Ip / port / alarmPlanName（预案）
├── eventCollection：原始事件列表（数组！一条故障单聚合多次事件）
│   └── event_lists[]：
│       ├── RawDataMap ← 原始观测（OID/正则命中全文/if_name/rate_value）
│       ├── sysLogStrategy（syslog 族）← 完整规则体：keyword/regular[]/recover_period/touch_count
│       ├── analysis_map（指标族）：touch_info（300秒内出现1次）+ expression_info（大于阈值70%）
│       ├── unique_message = rule_id:ip:port[:direction]（去重键）
│       └── 设备上下文：SN/厂商/型号/pod/datacenter/port_role（T2-T1）
├── work：工单（网络域独有）
│   ├── workStatus / callbackResult（人工止损中）
│   ├── disposalOpinion ← 自动化处置建议
│   ├── information ← ⭐链路 A/B 双端（见二.2）
│   └── operationRecord[]：操作流水（新故障→通知→自动化通告→AI排障→处置建议→人工止损）
```

## 二、关键发现

### 2.1 A→P 换算定论（2026-11-19，既有系统 NetworkLevel 映射，权威）
A1→P0、A3→P1、A5→P2、A7→P3、A9→P4。两样本 A5=P2（中等）。波分族策略多为 A9=P4（最低档）。⚠️ 线上 P 刻度为五档 P0-P4（网络域满量程），服务器域 S 四档只映射到 P0-P3；电力域另有 L1-L4 刻度（同 S 四档结构）。

### 2.2 ⭐链路 A/B 双端信息（网络域最大特色，拓扑金矿）
work.information 携带**链路两端设备全量信息**（port down 样本）：
- A 端：LFG13_F401_C04_POD026_T1_007（T1，H3C，廊坊A1，HundredGigE1/5/16，aIsFault=1）
- B 端：LFG19_F606_A07_POD000_T2_052（T2，H3C，廊坊A8，HundredGigE1/1/12，bIsFault=1）
- 两端跨机房（A1↔A8）！port_group_name="廊坊娄庄路A1_POD026_CORE-廊坊娄庄路A8_Border(大数据)"——**端口组名即链路标识**
→ **链路对象（跨域对齐的"同一物理事件"）在 netops 告警里自带双端定位**——`net.port.down ↔ server.nic.link_down` 关联边的建边数据源就是它，无需查拓扑库。

### 2.3 采集通道两族实证（与服务器域四族对照）
- 指标轮询族（traffic_port）：SNMP OID `.1.3.6.1.2.1.31.1.1.1.10.55`（ifHCOutOctets），agent 5 分钟粒度（interval=299998），rate_value=73.16%（已换算利用率）
- syslog 族（syslog_port）：`%%10IFNET/3/PHY_UPDOWN` + **regular 三式**（`PHY_UPDOWN:.+interface (.+?) .+down`）+ keyword 机制 + recover_period=300（恢复窗口 5 分钟）——规则体与服务器域 cloudbee 的 extract 族同构但内嵌在事件里
→ 字典映射键实证：`rootCause`（=category）+ 策略名 + RawDataMap（指标值/正则命中）。

### 2.4 AI 排障与自动验证闭环已存在（重要——OKB 定位参考）
operationRecord 流水实证：
- "AI排障任务触发成功"→"已生成处置建议"——**处置建议自动生成**（流量超限："保持关注并在群中@相关值班人"；端口Down："端口状态连续5次查询后状态均为UP，期间无新事件产生"）
- **自动验证**：端口 Down 后系统**主动查询端口状态 5 次均为 UP**→判断恢复（"判断次数失败无需通告"）
→ 网络域已有"告警→验证→自动恢复判定"的闭环雏形；OKB 的诊断字典价值 = 给这个 AI 排障喂结构化故障知识（它的处置建议目前是模板句）。

### 2.5 工单生命周期（网络域独有，服务器域告警无 work 层）
新故障→通知发送→自动化通告→AI排障→处置建议→人工止损中（workStatus=1/callbackResult/stopStartTime/stopSuccessOperator）→（止损成功/工单结束）
→ 复盘数据源直接可得：work + operationRecord 就是网络域的"故障处理过程记录"。

### 2.6 去重键与方向维度
uniqueKey = `fault_type_id:ip:port[:direction]`（173:10.237.80.3:40GE1/0/3:out / 143:172.21.177.172:HundredGigE1/1/12）——**流量类带方向**（in/out），端口类无。方向进实例属性。

### 2.7 字典勘误
- `net.traffic.overrun` 阈值观测面补 70% 变体（5分钟-业务线-OUT >70%）——默认 75%、业务线定制 70%，均为实例属性
- `net.port.down` 观测面补 H3C PHY_UPDOWN 正则族实证（severity=3/facility=23）；**端口状态异常策略实为 updown 事件**（含 down 与 up 变体，恢复靠 recover_period+touch_count 抑制）

## 三、与服务器域结构对照

| 维度 | cloudbee（服务器） | netops（网络） |
|---|---|---|
| 顶层 | 告警实例（alert_state 1/2） | **故障单**（faultId+work 工单+操作流水） |
| 原始数据 | etl_data.fault_info 单条 | eventCollection 事件列表（聚合） |
| 规则体 | strategy.extract（独立实体） | sysLogStrategy 内嵌事件（regular/keyword/recover_period） |
| 拓扑上下文 | cmdb_extend（本机全量） | **information 链路 A/B 双端**（跨设备） |
| 等级 | S1-S4→P0-P3（已定论） | A1-A9→?（待样本） |
| 自动化 | 无（is_push_repair 推送标志） | **AI排障+自动验证+处置建议闭环** |

## 四、待办

1. ~~A1/A3/A7/A9 各档样本（定 A→P）~~ 已定论（既有系统 NetworkLevel 映射，2026-11-19）
2. 网络域探活/波分/BGP 告警样本（补齐 rootCause 族映射实证）——**IB 首样本已到（2026-11-19）**：Link is Down（Mellanox UFM，A7=P3，北京中云信 POD002 LEAF-SPINE）。IB 特有形态：无 ip/port，定位靠 cluster_id（UFM 集群）+ short_message 内嵌双端交换机:端口 + GUID 对 + **cable S/N**（线缆级定位，比数通精细）；**集成缺口实证**：B 端设备解析失败（GUID 映射不到 CMDB）+ "推送故障影响范围失败:ip和端口都为空"；另新见 workStatus=5（批量结单）、群聊通知（nims_group_notify_result）、**通告等级 L1**（通告刻度与告警刻度 A/P 分立，又一刻度体系）
3. OKB 对接点评估：netops 的 AI 排障处置建议 ↔ 诊断字典（模板句→结构化诊断的升级路径）
4. 电力域预告：既有系统已有 PowerLevel 映射（L1→P0…L4→P3）——电力域数据接入时直接使用（注意：IB 样本的"通告等级 L1"是通告刻度，勿与电力 L 刻度混淆）
