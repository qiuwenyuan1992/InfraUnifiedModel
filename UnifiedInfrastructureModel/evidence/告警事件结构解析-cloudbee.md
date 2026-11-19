# cloudbee 告警事件结构解析（基于两条真实事件：Xid 154 + PSU Fault）

> 样本1：GPU Xid 154（Node Reboot Required），Lenovo WA5480G3，宿迁，agent syslog 族。
> 样本2：PSU3 Fault（T30alertPsuFault），Nettrix X660 G45，廊坊，**snmptrap 族**——2026-11-19 补。
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

### 2.2 策略生态有三族，926 条不是全部
- 指标表达式族：`server_agent_gpu` 等 category，926 条搜索接口已抓，232 条带表达式
- agent syslog 族：`server_agent_syslog`（Xid 154 样本，rule_id=2646）——正则匹配带内日志
- **snmptrap 族**：`server_snmp_syslog`（PSU 样本，source="bmc-snmptrap-collect"，rule_id=2067）——**T 编号 trap 命名体系**（T30alertPsuFault；与策略挖掘期所见 T5/T6/T66/T146/T281 MCE 同体系，T 系编号是 trap 的统一简写名）
- rule_id/keyword_id/syslog_rule（extract/keyword 实体）均**不在 926 条里**——独立的 syslog/trap 规则库接口未抓取

### 2.3 slot 抽取：成功与失败的两极（正则设计决定成败）
- **失败**（Xid 154 样本）：`slot="未知槽位"`、translator 空——extract 规则的 extractPath=$.data 指错，PCI 地址（0000:16:00）在 fault_info 里却没抽
- **成功**（PSU 样本）：regular 用**命名捕获组** `PSU(?<PSU>.*)_Status Critical...`，slot=3、translator=3 正确抽出
→ 结论：slot 翻译不是系统性缺口，是**规则质量问题**——命名捕获组是正确姿势，Xid 族的规则需要按 PSU 模式返修

### 2.4 CMDB 上下文全量自带（拓扑关联零成本）
`cmdb_extend` 一条告警带出：idc/楼栋/房间/机柜、SN、eth_ip+bmc_ip 双栈、厂商/型号、BIOS/BMC 固件版本、业务归属、部门、运营组、device_type。
另见 PSU 样本：`operation="故障"`（CMDB 运营状态直接反映故障态）。
→ 告警事件**自带机房→机柜→设备→IP→组织**的完整定位链，拓扑图谱关联可直接吃。

### 2.5 等级映射实证（已修正）
- 样本1：S3-中等 ↔ P2；样本2：S1-严重 ↔ P0
- **正确规律：P = S − 1**（S1→P0、S2→P1、S3→P2、S4→P3）
- ⚠️ 修正记录：本文档曾误推 S(n)→P(5-n)，因 S3 时两公式巧合同值；PSU 样本 S1→P0 证伪旧公式（2026-11-19 修正）

### 2.6 线上值域 ≠ 文档值域（154 的教训）
字典首轮从 XID 文档挑的 9 码没有 154，而线上真实打出的第一条就是 154。
→ **字典收口前必须用告警事件流校验值域**；Xid 154 两个恢复变体（0x1 GPU Reset / 0x2 Node Reboot）已实证。

### 2.7 云蜂已有"机柜掉电聚合"（PSU 样本新发现，重要）
PSU 样本顶层带 `group: {name: "单机柜维度-掉电聚合_单机柜掉电聚合"}`——**多台 PSU 故障聚合成机柜级掉电事件已在云蜂落地**。
→ 直接印证字典中 `server.psu.input_lost ↔ rack.power.outage` 跨域标注：工程上已有对应聚合器。
→ 图谱层的"多 PSU 告警 → 机柜掉电事件"边可以对接这个聚合组，不必重建。

### 2.8 生命周期字段（假设待验证，仅两样本）
- `alert_state`: 1（Xid 样本，活动中）/ 2（PSU 样本，operator 有人、is_push_repair=1、update 晚 15min）——**假设**：2=已处理/恢复；需事件流确认
- `is_push_repair`: 1（推送维修）/ 2（Xid 样本未推）——语义待事件流确认
- PSU regular 尾缀 `Assert NULL`——trap 的 assert（激活）模式；恢复告警应对应 deassert 或独立恢复规则，与字典"自愈/恢复对"呼应

## 三、告警 → 字典编码 映射方案（定型）

| 判定路径 | 键 | 例子 |
|---|---|---|
| agent syslog 族 | strategy.category 或 regular 提码 | "GPU_XID_154_GPU Reset Required" → `gpu.xid.154`（变体按 fault_info 0x1/0x2 分级） |
| snmptrap 族 | strategy.category（T 编号 trap 名）+ slot 捕获组 | "T30alertPsuFault" + slot=3 → `server.psu.fault`（slot 进实例属性不进编码） |
| 指标族 | 策略名 + 表达式值 | "Exporter-GPU-XID 48"（DCGM_FI_DEV_XID_ERRORS==48）→ `gpu.xid.48` |
| 兜底 | fault_type + fault_parts | "GPU故障"+GPU → 候选集（gpu.*），需 fault_info 二次解析 |

## 四、待办

1. 抓 syslog 规则库接口（rule_id/keyword_id/syslog_rule 是独立实体，估计有 list API）
2. 用告警事件流批量校验字典值域（尤其 NPU npu_chip_info_error_code 的 40+ 实战值）
3. PCI 地址→槽位翻译表（按机型）：告警事件可做，做了 slot 不再"未知槽位"
