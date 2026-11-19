# cloudbee 告警事件结构解析（基于 2026-11-19 Xid 154 真实事件）

> 样本：GPU Xid 154（Node Reboot Required 变体），Lenovo WA5480G3，宿迁电信长线分局。
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

### 2.2 策略生态有两族，926 条不是全部
- 指标表达式族：`server_agent_gpu` 等 category，926 条搜索接口已抓，232 条带表达式
- **syslog 规则族**：`server_agent_syslog`，本事件 rule_id=2646 / syslog_rule=813 / keyword_id=7751 **不在 926 条里**——cloudbee 还有独立的 syslog 规则库接口（extract/keyword 是独立实体）未抓取

### 2.3 XID 码在 fault_info 里，slot 翻译是缺口
- fault_info 含 `Xid (PCI:0000:16:00): 154`——码与 PCI 地址都在原文里
- 但 `slot="未知槽位"`、`fault_slot_translator=""`——**PCI 地址→物理槽位翻译未做**（运维痛点，槽位定位靠人工查）
- extract.params 定义了 slot_list 抽取但样本里没抽出来（extractPath=$.data 为空）

### 2.4 CMDB 上下文全量自带（拓扑关联零成本）
`cmdb_extend` 一条告警带出：idc/楼栋/房间/机柜（宿迁电信长线分局B1/B1-F2-05A-02）、SN、eth_ip+bmc_ip 双栈、厂商/型号（LENOVO WA5480G3）、BIOS/BMC 固件版本、业务归属（商城）、部门、运营组（TPAAS_NDOS）、device_type（GPU服务器）。
→ 告警事件**自带机房→机柜→设备→IP→组织**的完整定位链，拓扑图谱关联可直接吃。

### 2.5 等级映射实证
S3-中等 ↔ P2：`fault_level=3` ↔ `alert_level=P2`，即 S(n) → P(5-n)。

### 2.6 线上值域 ≠ 文档值域（154 的教训）
字典首轮从 XID 文档挑的 9 码没有 154，而线上真实打出的第一条就是 154。
→ **字典收口前必须用告警事件流校验值域**；Xid 154 两个恢复变体（0x1 GPU Reset / 0x2 Node Reboot）已实证。

## 三、告警 → 字典编码 映射方案（定型）

| 判定路径 | 键 | 例子 |
|---|---|---|
| syslog 族 | strategy.category 或 regular 提码 | "GPU_XID_154_GPU Reset Required" → `gpu.xid.154`（变体按 fault_info 0x1/0x2 分级） |
| 指标族 | 策略名 + 表达式值 | "Exporter-GPU-XID 48"（DCGM_FI_DEV_XID_ERRORS==48）→ `gpu.xid.48` |
| 兜底 | fault_type + fault_parts | "GPU故障"+GPU → 候选集（gpu.*），需 fault_info 二次解析 |

## 四、待办

1. 抓 syslog 规则库接口（rule_id/keyword_id/syslog_rule 是独立实体，估计有 list API）
2. 用告警事件流批量校验字典值域（尤其 NPU npu_chip_info_error_code 的 40+ 实战值）
3. PCI 地址→槽位翻译表（按机型）：告警事件可做，做了 slot 不再"未知槽位"
