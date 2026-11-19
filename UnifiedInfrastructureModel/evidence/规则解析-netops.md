# netops 网络域告警规则解析（2026-11-19，规则层全量复盘）

> 数据：raw/network/netops_strategies.json（312 条）。
> 结论先行：**规则双轨制定论**——312 条 = 250 指标表达式族 + 62 syslog 正则族，两族判定/恢复机制完全不同。

## 一、规则双轨制

| 维度 | 指标表达式族（250 条） | syslog 正则族（62 条） |
|---|---|---|
| category | traffic_* / crc / drop / cpu / mem / ping / session / nq_finance / burst / telemetry | syslog（55）/ wdm_syslog（6）/ ib_alarm（1） |
| 判定 | alarm_expressions：op+value（无 path，语义在 category） | sysLogStrategy.regular[] 正则 + keyword（规则体内嵌告警事件，非策略搜索接口字段） |
| 观测源 | SNMP OID 轮询（ifHCOutOctets 等）/ telemetry | 设备 syslog（IFNET/3/PHY_UPDOWN 等） |
| 恢复 | recover_expressions（镜像阈值：>75% ↔ <=75%） | 47 条无恢复周期（靠去重/人工关闭）+ 9 条 300s 窗口 + 6 条 60s×1 次（端口Down 类） |
| 字典映射键 | category + 策略名 + 阈值 | category（=策略名主体）+ 正则命中（RawDataMap.message） |

**62 条"空表达式"之谜定论**：syslog/wdm_syslog/ib_alarm 三类没有 alarm_expressions **是设计使然**（正则族不走表达式引擎），不是数据缺失。此前首轮分析未点破，特此澄清。

## 二、指标族阈值类型学（250 条）

- **利用率**：出口/专线 >75%（默认）、业务线定制 70%、托管区万国 >90%
- **计数类**：CRC >=83（默认）/ >=2（专线接入区）、drop >33、队列 DROP >2×900s
- **断言**：探活 ==0（触发）/ ==1（恢复）
- **环比/突变**：出口环比下降 <80%、专线陡降 >0.01、掉零 <99.99
- **绝对带宽**：北京-荷兰 >7500Mbps 等专线定制
- **包守恒平衡区间（新解）**：多值 "70,110" 是**入出比正常区间**（70%-110%）——出界即告警；PR 设备 70,110 / LE/CISCO 80,120 / 自监控 95,105 / 默认 90,110。厂商间阈值差异大（自监控最严），印证"厂商不进编码、作规则属性"

## 三、syslog 族规则细部（62 条）

- **keyword 机制**：PHY_UPDOWN、BGP、OSPF、BFD 等关键词命中后走 regular 多式提取（端口 Down 样本实证三式）
- **恢复三级制**：无恢复周期（47 条，硬件/协议类——坏了不会自愈）/ 300s 窗口（9 条）/ 60s×1（6 条，端口状态类——updown 抖动频繁，快速自动恢复）
- **A1 档在 syslog 族的两条**：锐捷端口 updown 异常延迟、设备端口状态异常【T1】——**端口状态类是网络域最高敏感度**（对照：金融专线中断 A1 在 nq_finance 族）

## 四、字典与规则的对账结论

61 码全部有规则族对应，**无因规则遗漏的漏码**。复核增补记录：
- `net.traffic.latency`（专线延迟）——nq_finance 族规则在位
- `net.line.probe.fail`（专线中断 A1）——同族
- 包守恒观测面更新为平衡区间语义（70,110）
- `net.traffic.burst` 观测面并入 telemetry 10s 采集

**规则层对字典的唯一新增语义**：syslog 族恢复三级制（无/300s/60s）——建议作为字典"自愈倾向"属性沉淀（端口类快速自愈 vs 硬件类不自愈），供诊断分流参考。

## 五、遗留（数据无法回答）

- wdm_syslog 6 条的正则内容不在搜索接口（需 wdm 系统侧数据）
- ib_alarm 1 条（IB 系统推送告警）同理
- 多值表达式引擎语义（"90,110" 中第二个值的确切含义——80% 置信是区间上界，需 wdm/包守恒文档确认）
