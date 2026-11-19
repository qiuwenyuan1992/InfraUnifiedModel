# cloudbee 告警事件结构解析（基于四条真实事件：Xid 154 + PSU Fault + 风扇 Lower Critical + 硬盘 offline）

> 样本1：GPU Xid 154（Node Reboot Required），Lenovo WA5480G3，宿迁，agent syslog 族。
> 样本2：PSU3 Fault（T30alertPsuFault），Nettrix X660 G45，廊坊，snmptrap 族——2026-11-19 补。
> 样本3：风扇 M_Fan1_Speed_F Lower Critical（0.00 RPM），安擎 Server-8621-G6，张家口，**bmc syslog 族**——2026-11-19 补。
> 样本4：硬盘 sd 0:0:28:0 rejecting I/O（offline device），Inspur NF5466M5，北京，agent syslog 族——2026-11-19 补。
> 三层数据齐套后的最后一块：**告警实例层**。本文档是"告警 → 字典编码"映射设计的结构依据。

## 一、事件三层结构

```
告警事件
├── 顶层：告警实例元数据（压缩/去重后的事件）
│   ├── alert_name = "GPU故障"        ← 即 fault_type 名（粗分类，非策略名！）
│   ├── alert_level = "P2" / fault_level=3 / fault_level_info="S3-中等"
│   ├── sn / fault_parts / slot / fault_type_id
│   ├── alert_time / latest_alert_time（首末次告警时间→重复告警聚合）
│   └── alert_state=1（活动中）/ alert_type / is_push_repair
├── resource：定位四元组（fault_parts / fault_type_id / slot / sn）
└── etl_data：原始数据层（信息量最大）
    ├── fault_info ← 原始 syslog 全文（XID 码就在这里！）
    │   "kernel: NVRM: Xid (PCI:0000:16:00): 154, GPU recovery action changed from 0x0 (None) to 0x2 (Node Reboot Required)"
    ├── regular = ["Xid.*154.*to.*Node Reboot Required"]  ← 命中的正则
    ├── strategy
    │   ├── category / strategy_name = "GPU_XID_154_GPU Reset Required"
    │   ├── application：feature "@rule@2646"、rule_id=2646、keyword_id=7751、syslog_rule=813
    │   └── extract：正则抽取规则（name=Xid_154, params: slot_list ← $.data）
    ├── cmdb_extend ← 全量 CMDB 上下文（见下）
    └── event_rule_type = "server_agent_syslog" / source = "cloudbee_agent"
```

## 二、关键发现（影响映射设计）

### 2.1 alert_name 不能做映射键
`alert_name` 是 fault_type（"GPU故障"）——粒度太粗。真实映射键组合：
- **syslog 族**：`strategy.category`（=策略名）+ `regular`（命中正则）+ `fault_info` 解析（含 XID 码/PCI 地址）
- **指标族**（前轮 232 条）：策略名 + alarm_expressions

### 2.2 采集通道有四族，926 条不是全部
- 指标表达式族：`server_agent_gpu` 等 category，926 条搜索接口已抓，232 条带表达式
- agent syslog 族：`server_agent_syslog`（Xid 154 / 硬盘 offline 样本，source=cloudbee_agent）——正则匹配**带内**日志
- bmc syslog 族：`server_bmc_syslog`（风扇样本，source=bmc-syslog-collect）——**带外** BMC 日志直采；fault_info 是 IPMI SEL 标准格式（`[Event] Lower Critical going low | [0x8c] M_Fan1_Speed_F | 0.00 RPM | Fan | State Asserted`，含传感器号 0x8c）
- snmptrap 族：`server_snmp_syslog`（PSU 样本，source=bmc-snmptrap-collect，rule_id=2067）——**T 编号 trap 命名体系**（T30alertPsuFault；与策略挖掘期所见 T5/T6/T66/T146/T281 MCE 同体系）
- rule_id/keyword_id/syslog_rule（extract/keyword 实体）均**不在 926 条里**——独立的 syslog/trap 规则库接口未抓取
- **注意**：`event_rule_type`/`from_type`/`creator_name`（云蜂监控cloudbee / snmptrap / syslog）三处都标识通道，值域一致可互验

### 2.3 slot 有两层语义：原文抽取 vs 物理槽位换算（四样本定论）
- **抽取层**（translator）：从 fault_info 用正则抽的**原文位置标识**——PSU 命名捕获组抽 "3"、硬盘抽 SCSI 地址 "0:0:28:0"、风扇抽传感器名 "M_Fan1_Speed_F"（extract 描述明说"不换算物理槽位"）
- **换算层**（slot 物理槽位）：抽取标识 → 物理槽位的映射，PSU 样本换算成功（slot=3），硬盘样本没换算（slot=""，SCSI 地址留在 translator），Xid 样本两层全空
- 抽取失败原因各异：Xid 族 extractPath=$.data 指错；风扇是策略性选择（观察规则故意不换算）
→ **映射设计**：translator 进实例属性；物理槽位换算按机型建表（PCI/SCSI/传感器名 → 槽位），是云蜂与图谱都可消费的增值层

### 2.4 CMDB 上下文全量自带（拓扑关联零成本）
`cmdb_extend` 一条告警带出：idc/楼栋/房间/机柜、SN、eth_ip+bmc_ip 双栈、厂商/型号、BIOS/BMC 固件版本、业务归属、部门、运营组、device_type。
另见 PSU/硬盘样本：`operation="故障"`（CMDB 运营状态直接反映故障态）。
→ 告警事件**自带机房→机柜→设备→IP→组织**的完整定位链，拓扑图谱关联可直接吃。

### 2.5 等级映射实证（四样本三档全中，定论）
- 样本1：S3↔P2；样本2/3：S1↔P0；样本4：S4↔P3
- **定论：P = S − 1**（S1→P0、S2→P1、S3→P2、S4→P3）
- ⚠️ 修正记录：本文档曾误推 S(n)→P(5-n)，因 S3 时两公式巧合同值；PSU 样本 S1→P0 证伪旧公式（2026-11-19 修正，风扇/硬盘样本再验证）

### 2.6 线上值域 ≠ 文档值域（154 的教训）
字典首轮从 XID 文档挑的 9 码没有 154，而线上真实打出的第一条就是 154。
→ **字典收口前必须用告警事件流校验值域**；Xid 154 两个恢复变体（0x1 GPU Reset / 0x2 Node Reboot）已实证。

### 2.7 云蜂已有"机柜掉电聚合"（PSU 样本新发现，重要）
PSU 样本顶层带 `group: {name: "单机柜维度-掉电聚合_单机柜掉电聚合"}`——**多台 PSU 故障聚合成机柜级掉电事件已在云蜂落地**。
→ 直接印证字典中 `server.psu.input_lost ↔ rack.power.outage` 跨域标注：工程上已有对应聚合器。
→ 图谱层的"多 PSU 告警 → 机柜掉电事件"边可以对接这个聚合组，不必重建。

### 2.8 生命周期字段（硬盘样本定论）
- `alert_state`: **1=活动中，2=已处理/关闭**（定论：硬盘样本 state=2 + operator=王铮 + is_push_repair=1；风扇样本 state=1 无 operator）
- 重复告警聚合：硬盘样本 alert_time=11-01 22:35，latest_alert_time=11-03 15:52——**同告警持续两天**，首末时间戳分开存
- `is_push_repair`: 1=已推维修 / 2=未推；`strategy_push`: 1=推送 / 2=不推送（值语义待更多样本，方向已明）
- 处理人字段：`operator`（PSU=王博伟 / 硬盘=王铮，真实值班人；"server" 为系统占位）

### 2.9 恢复告警的工程机制：排除法而非独立规则（风扇样本实证，重要）
风扇 regular 带**负向前瞻排除恢复词**：`(?!.*(?:Fan\d+, Lower|de.?assert|cleared|recovered|restored|no longer in|to normal|normal levels))`
- 恢复事件词表实证：**deassert / cleared / recovered / restored / no longer in / to normal / normal levels**——恢复告警与故障告警走**同通道同格式**，靠词表区分
- IPMI 事件尾缀 `State Asserted` 与 PSU trap 尾缀 `Assert NULL` 同理——assert/deassert 是 BMC 侧统一语义
→ 对字典的含义：`server.selfheal.*`（自愈事件对）的观测面 = 同族正则的 deassert/恢复词命中；告警系统已把恢复词挡在故障告警外，**恢复事件流需要单独的规则去接**（当前可能丢弃）

### 2.10 策略对象上的三个状态过滤器（硬盘样本新发现）
monitorObject 里有值的三字段（其余样本全空）：
- `fault_status: "已认定"`——故障池认定状态
- `fault_result_status: "交付"`——处置去向
- `online_failure: "在线故障"`——在线/离线故障分类
→ 这些是**策略生效维度**（按故障处理阶段过滤推送对象），非告警实例属性；但值域（已认定/交付/在线故障…）是工单流转状态机的词汇，将来对齐复盘数据时有用

## 三、告警 → 字典编码 映射方案（定型）

| 判定路径 | 键 | 例子 |
|---|---|---|
| agent syslog 族 | strategy.category 或 regular 提码 | "GPU_XID_154_GPU Reset Required" → `gpu.xid.154`（变体按 fault_info 0x1/0x2 分级）；"rejecting I/O to offline device" → `storage.disk.fault` |
| bmc syslog 族 | IPMI SEL 事件词 + 传感器名（0x8c/M_Fan1_Speed_F） | "Lower Critical going low"+Fan 传感器 → `server.fan.underspeed`（0.00 RPM 停转） |
| snmptrap 族 | strategy.category（T 编号 trap 名）+ slot 捕获组 | "T30alertPsuFault" + slot=3 → `server.psu.fault`（slot 进实例属性不进编码） |
| 指标族 | 策略名 + 表达式值 | "Exporter-GPU-XID 48"（DCGM_FI_DEV_XID_ERRORS==48）→ `gpu.xid.48` |
| 兜底 | fault_type + fault_parts | "GPU故障"+GPU → 候选集（gpu.*），需 fault_info 二次解析 |

## 四、待办

1. 抓 syslog 规则库接口（rule_id/keyword_id/syslog_rule 是独立实体，估计有 list API）
2. 用告警事件流批量校验字典值域（尤其 NPU npu_chip_info_error_code 的 40+ 实战值）
3. PCI 地址→槽位翻译表（按机型）：告警事件可做，做了 slot 不再"未知槽位"
