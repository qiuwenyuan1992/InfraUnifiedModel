# 因果边清单（draft 种子数据）

> 生成：2026-11-20。来源：《跨域关联全景.md》12 组对齐 + 12 场景（A-L）+ 三域字典内标注的因果对——**形态转换，不新建知识**。
> 消费方式：OKB 摄取时的 causal_rule 种子。按 OKB 纪律：**全部 draft、AI 提候选、人定稿**；curated 升级需 ≥2 独立工单印证（当前 0 条满足）。
> 字段：source → target | edge_kind | conditions | time_constraints（秒/分/时档）| evidence | 出处。evidence：`runtime`（真实告警实证）/ `infer`（结构推断）/ `ind`（公开行业案例）。

## 一、same_event 边（同一物理事件两侧观测，双向语义，trace 不遍历）

| source ↔ target | 条件 | evidence | 出处 |
|---|---|---|---|
| `server.nic.link_down` ↔ `net.port.down` | 同链路（对端匹配） | infer | 全景对齐 #1 |
| `server.psu.input_lost` ↔ `power.rack.pdu.output_lost_dual` | 同机柜 | infer（物理供配电决定） | 全景 #6 |
| `server.psu.redundancy_lost` ↔ `power.rack.pdu.output_lost_single` | 同机柜 | infer | 全景 #9 |
| `net.device.psu.fault` ↔ `power.rack.pdu.output_lost_dual` | 同机柜（TOR） | infer | 全景 #10 |
| `server.nic.ib.fabric` ↔ `net.ib.link.down` | 同 UFM 集群（cluster_id） | runtime | 全景 #5 + IB 样本 |
| `power.battery.discharge`（事件） ↔ `power.ups.input_lost`（事件同窗） | 同机房同 UPS | runtime（两事件同窗实证于样本结构） | 场景 A |

## 二、same_family 边（机理同族对比，trace 不遍历）

| source ↔ target | 对比语义 | evidence | 出处 |
|---|---|---|---|
| `server.optical.rx_power.abnormal` ↔ `net.port.optical_power.abnormal`（预留） | 双侧 DOM 功率对比定位劣化端 | infer | 全景 #2 |
| `server.nic.crc_error` ↔ `net.port.crc_error` | 双侧 CRC：都升⇒链路坏；单侧⇒该侧问题 | infer | 全景 #3 |
| `power.cdu.leak` ↔ `server.liquid.leakage` | 源头先报 vs 末端后报；同时报⇒大范围 | runtime（CDU 漏液样本） | 全景 #7 |
| `server.sys.*`（探活预留） ↔ `net.ping.*.fail` | 失联语义对齐：设备挂 vs 通道断 | infer | 全景 #4 |

## 三、causal 边——电力链（场景 A/D/F，秒级）

| source → target | conditions | time | evidence | 出处 |
|---|---|---|---|---|
| `power.grid.mains.abnormal` → `power.ups.input_lost` | — | seconds | infer | 场景 A 电力链 |
| `power.ups.input_lost` → `power.battery.discharge` | — | seconds | infer | 场景 A |
| `power.grid.mains.abnormal` → `power.genset.start` | 市电中断持续 | seconds-minutes | infer | 柴发策略语义 |
| `power.battery.discharge` + `power.genset.fuel_low` → `power.ups.output_lost` | 兜底链任一环失效（启机失败/ATS 切换失败/燃油耗尽） | minutes-hours | infer | 场景 F 两级时钟 |
| `power.grid.mains.abnormal` + `power.ups.mode.abnormal`（旁路） → `power.ups.output_lost` | **冗余态放大器**：旁路无保护扛浪涌 | seconds | infer | 全景原则 9 |
| `power.genset.start_fail`（预留） → `power.ups.output_lost` | 市电异常窗内 | seconds-minutes | infer | 预留位语义 |
| `power.ups.output_lost` → `power.rack.busbar.input_lost` | — | seconds | infer | 供配电树 |
| `power.rack.busbar.input_lost` → `power.rack.pdu.output_lost_dual` | — | seconds | infer | 供配电树 |
| `power.rack.pdu.output_lost_dual` → `net.port.down` | 同柜 TOR 下联口批量 | seconds | infer | 场景 D（网络侧放大） |
| `power.rack.pdu.output_lost_dual`（恢复窗） → `power.rack.cabinet.over_tech` | **恢复涌流**：掉电恢复后 N 分钟内多机集中上电 | seconds | infer | 场景 I 上电风暴 |
| `server.psu.input_lost` → `server.sys.crash.unexpected` | 双路失/无冗余 | seconds | runtime | PSU 样本 + 掉电聚合 |

## 四、causal 边——制冷链（场景 B/E，分级时钟）

| source → target | conditions | time | evidence | 出处 |
|---|---|---|---|---|
| `cooling.chilled_water.pump_fault` → `cooling.chiller.fault` | 冷冻水联锁保护 | seconds-minutes | infer | 字典制冷族语义 |
| `cooling.cooling_water.pump_fault` → `cooling.chiller.fault` | 冷凝高压保护 | minutes | infer | 字典制冷族语义 |
| `cooling.chiller.fault` → `cooling.it_room.temp_mid` | 蓄冷缓冲消耗 | minutes-hours（**蓄冷倒计时**） | ind（行业案例：集中式失效 10 小时级） | 场景 E |
| `cooling.it_room.temp_mid` → `cooling.it_room.temp_high` | 持续不干预 | tens of minutes | infer | 三档温度策略递进 |
| `cooling.it_room.temp_high` → `cooling.it_room.temp_critical` | 持续不干预 | — | infer | 三档递进 |
| `cooling.it_room.temp_critical` → `server.thermal.temp.critical` | — | minutes | infer | 全景 #8 因果链 |
| `cooling.it_room.temp_critical` → `net.device.thermal.alarm` | — | minutes | infer | 全景 #11 |
| `server.thermal.temp.critical` → `gpu.perf.degrade` | GPU 热保护降频 | seconds | infer（CLOCK_THROTTLE 位掩码实证降频机制） | GPU 字典 |
| `cooling.it_room.temp_critical`（持续极值） → `env.room.smoke`（预留） | 温升失控次生火灾 | hours | ind（香港 2022 leak→fire 级联实证） | 场景 E |

## 五、causal 边——液冷链（场景 H，分钟级）

| source → target | conditions | time | evidence | 出处 |
|---|---|---|---|---|
| `power.cdu.power.abnormal` → `power.cdu.pump.fault_secondary` | 供电异常致泵停 | seconds | infer | CDU 族语义 |
| `power.cdu.pump.fault_secondary` → `power.cdu.supply_temp.abnormal_secondary` | — | minutes（**液冷分钟钟**） | infer | 场景 H |
| `power.cdu.supply_temp.abnormal_secondary` → `server.thermal.temp.critical` | 无风冷备份 | minutes | infer | 场景 H（区别于风冷小时级） |

## 六、causal 边——网络域内与协议链（秒级）

| source → target | conditions | time | evidence | 出处 |
|---|---|---|---|---|
| `net.device.psu.fault` → `net.port.down` | 本机下联口批量 | seconds | infer | 网络字典硬件族 |
| `net.protocol.bfd.down` → `net.protocol.bgp.peer_down` | BFD 快速检测先行 | seconds | infer | 协议族语义（BFD=第一现场） |
| `net.port.flapping` → `net.port.down` | 劣化累积终态 | minutes | infer | 端口族语义 |
| `net.port.down` → `net.traffic.sudden_drop` | 承载业务流量 | seconds | infer | 性能族 |
| `net.port.down` → `net.traffic.zero` | 单上联/全部承载口断 | seconds | infer | 性能族 |
| `net.device.psu.fault`（MGT 角色） → `server.sys.bmc.unreachable`（预留） | device_role=MGT 批量 | seconds | infer | 场景 J 管理面 |

## 七、causal 边——服务器域内（字典已标注因果对）

| source → target | conditions | time | evidence | 出处 |
|---|---|---|---|---|
| `server.cpu.tsc` → `server.cpu.clock.switch.hpet` | TSC 不稳触发时钟源切换 | seconds | runtime | CPU 字典因果对 |
| `storage.disk.fault` → `storage.vd.degraded` | — | seconds-minutes | runtime（PD→VD 状态机实证） | 存储字典因果链中游 |
| `server.board.backplane.fault` → `storage.disk.fault` | 同背板多盘同时 | seconds | infer（多盘分流规则） | 背板码语义 |
| `server.fan.underspeed` → `server.thermal.temp.critical` | 风扇冗余不足 | minutes | infer | 风扇族语义 |
| `gpu.xid.154` → `server.sys.reboot.unexpected` | 恢复动作=0x2（Node Reboot） | minutes | **runtime**（Xid 154 样本实证 0x2 变体） | GPU 字典 + 样本 |
| `gpu.xid.154` → `gpu.reset.fail`（0x1 变体重启卡） | 恢复动作=0x1（GPU Reset） | minutes | infer | 154 双变体语义 |

## 八、消费与定稿说明

1. **本文档是种子边清单**，入库时逐条转为 causal_rule draft 断言；same_event/same_family 单行入库（双向查询）。
2. **证据分级映射**：runtime → `runtime_verified`（挂 alert_event 源）；infer → `inferred`；ind → `doc_explicit`（挂 industry_report 源，标注"行业案例推导"）。
3. **人定稿清单**（优先评审）：①场景 I 恢复涌流边（触发语义特殊）；②冗余态放大器边（组合升级规则是否符合预期诊断行为）；③same_event 组的"同机柜/同链路"匹配条件精度。
4. **不建边的**（遵守 OKB 边界）：判定序（三查筛查/空间聚类）、根因聚合元规则、影响清单（拓扑库职责）——留在全景文档供编排层消费。

## 九、查证补充（2026-11-21，网络检索核验）

| 边 | 依据 | 判定 |
| --- | --- | --- |
| `net.port.optical_power.abnormal → net.port.crc_error` | 华为 CloudEngine 维护宝典："光模块发光功率正常，但接口统计存在 CRC 等丢包——可能原因：光模块收光功率异常"（交换机侧直接结论）；中兴真实案例：100G 光模块 CRC 误码超限致业务闪断 | 成立，doc_explicit |
| `server.optical.rx_power.abnormal → server.nic.crc_error` | 同机理对称（光接收机 BER 物理特性），服务器侧为最小推断步 | 成立，draft 待定稿 |
| `server.optical.rx_power.abnormal → server.nic.link_down`（曾拟） | 未获文档支持：严重劣化走 LOS 告警，文档结论止于 CRC/丢包 | **不建**（防止编造） |
| `power.rack.cabinet.over_tech → power.psu.fault`（曾拟） | 涌流损伤对象是 IT 设备开关电源（SMPS 劣化），非动环 PSU 告警码 | **不建**，power.psu.fault 移出上电风暴场景 |
