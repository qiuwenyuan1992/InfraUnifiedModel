"""合成告警数据集：部位+故障类型+时间点 → 告警归一 → 诊断。

用户定位（2026-11-21）：未来的告警就是"什么部位发生什么类型故障、什么时间点"——
用合成数据集先把多场景诊断闭环跑通，真实告警流接入时只换输入源。

样本设计原则（不编造知识）：
- 覆盖 OKB 图上已有边的场景（掉电/制冷/光衰/GPU Xid/BGP），保证可推演
- 附带图上无路径的告警（T30 PSU/未登记），验证"知识空白"如实分支
- 每条样本自带期望（expected），既是测试断言也是场景演示数据
"""

from __future__ import annotations

# 告警归一映射：部位 + 故障类型 → OKB alert signature
# 真实告警流接入时：解析器产出 (部位, 类型) 二元组，此处映射到 stable_code 体系
ALERT_REGISTRY: dict[tuple[str, str], str] = {
    ("server.psu", "input_lost"): "SERVER-PSU-INPUT-LOST",
    ("power.psu", "fault"): "T30alertPsuFault",
    ("power.battery", "discharge"): "T30alertUpsBatteryDischarge",
    ("cooling.chilled_water_pump", "fault"): "ALERT-CHWP-FAULT",
    ("net.optical", "rx_power_abnormal"): "ALERT-OPTICAL-RX-LOW",
    ("gpu", "xid_154"): "GPU_XID_154",
    ("net.bgp", "peer_down"): "ALERT-BGP-PEER-DOWN",
    ("net.link", "down"): "ALERT-LINK-DOWN",
    ("net.port", "down"): "ALERT-LINK-DOWN",
    ("net.port", "flapping"): "ALERT-PORT-FLAPPING",
    ("net.port", "crc_error"): "ALERT-PORT-CRC-ERROR",
    ("power.busbar", "input_lost"): "ALERT-BUSBAR-INPUT-LOST",
}

# 合成告警样本：部位+类型+时间点+期望诊断行为
SAMPLES: list[dict] = [
    {
        "id": "sim-001",
        "label": "服务器 PSU 输入丢失",
        "site": "pod-A",          # fixture 拓扑站点（与 inventory.py 一致）
        "equipment": "server.psu",         # 部件：服务器电源
        "fault_type": "input_lost",        # 类型：输入丢失
        "timestamp": "2026-11-21T10:00:00Z",
        "raw_text": "服务器电源模块输入丢失告警",  # 将来 LLM 语义化的输入
        "signature": "SERVER-PSU-INPUT-LOST",
        "entity": "srv-001",             # fixture 拓扑实体（影响半径用）
        "expected": {
            "start_code": "server.psu.input_lost",
            "reaches": "server.sys.crash.unexpected",  # 主链应推进到宕机
            "pending": ["psu_redundancy"],               # 待验证：冗余是否已失
            "peers_include": "power.rack.pdu.output_lost_dual",  # 同事件对端
            "reported": True,
        },
    },
    {
        "id": "sim-002",
        "label": "冷冻水泵故障停机",
        "site": "pod-A",
        "equipment": "cooling.chilled_water_pump",
        "fault_type": "fault",
        "timestamp": "2026-11-21T10:05:00Z",
        "raw_text": "冷冻水泵故障停机",
        "signature": "ALERT-CHWP-FAULT",
        "entity": "pump-1",            # 制冷域占位实体（拓扑未同步，半径如实降级）
        "expected": {
            "start_code": "cooling.chilled_water.pump_fault",
            "reaches": "env.room.smoke",      # 5跳级联终点（演示 deep chain）
            "min_hops": 5,
            "reported": True,
        },
    },
    {
        "id": "sim-003",
        "label": "光模块接收光功率低",
        "site": "pod-A",
        "equipment": "net.optical",
        "fault_type": "rx_power_abnormal",
        "timestamp": "2026-11-21T10:10:00Z",
        "raw_text": "光模块接收光功率低于灵敏度阈值",
        "signature": "ALERT-OPTICAL-RX-LOW",
        "entity": "srv-101",
        "expected": {
            "start_code": "server.optical.rx_power.abnormal",
            "reaches": "server.nic.crc_error",   # 光衰→CRC（vendor_doc 查证边）
            "reported": True,
        },
    },
    {
        "id": "sim-004",
        "label": "GPU Xid 154 错误",
        "site": "pod-A",
        "equipment": "gpu",
        "fault_type": "xid_154",
        "timestamp": "2026-11-21T10:15:00Z",
        "raw_text": "GPU Xid 154：节点需要重启恢复",
        "signature": "GPU_XID_154",
        "expected": {
            "start_code": "gpu.xid.154",
            "reaches_any": ["server.sys.reboot.unexpected", "gpu.reset.fail"],
            "pending": ["recovery_action"],
            "reported": True,
        },
    },
    {
        "id": "sim-005",
        "label": "BGP 邻居 Down",
        "site": "pod-A",
        "equipment": "net.bgp",
        "fault_type": "peer_down",
        "timestamp": "2026-11-21T10:20:00Z",
        "raw_text": "BGP 邻居 Down，BFD 先行检测",
        "signature": "ALERT-BGP-PEER-DOWN",
        "entity": "bgp-1",             # 逻辑会话实体；观测面解析到承载端口对
        "expected": {
            "start_code": "net.protocol.bgp.peer_down",
            # bgp 是叶子节点（BFD→BGP 方向）：链为空是知识如实，不是失败
            "empty_chain": True,
            "surface_entities": ["spine-1:P1", "tor-A1:P1"],  # 本端+对端端口
            "reported": False,
        },
    },
    {
        "id": "sim-009",
        "label": "交换机端口 Down",
        "site": "pod-A",
        "equipment": "net.port",
        "fault_type": "down",
        "timestamp": "2026-11-21T10:40:00Z",
        "raw_text": "交换机端口 Link Down（TOR 上联口）",
        "signature": "ALERT-LINK-DOWN",
        "entity": "tor-A1:P1",
        "expected": {
            "start_code": "net.port.down",
            "reaches": "net.traffic.zero",          # 单上联条件：流量归零
            "pending": ["single_uplink"],
            "radius_entities": ["tor-A1:P1", "spine-1:P1"],  # 端口本端+链路对端
            "reported": True,
        },
    },
    {
        "id": "sim-010",
        "label": "端口震荡（链路不稳）",
        "site": "pod-A",
        "equipment": "net.port",
        "fault_type": "flapping",
        "timestamp": "2026-11-21T10:45:00Z",
        "raw_text": "端口在 1 分钟内反复 Up/Down（震荡）",
        "signature": "ALERT-PORT-FLAPPING",
        "entity": "tor-A1:P1",
        "expected": {
            "start_code": "net.port.flapping",
            "reaches": "net.traffic.sudden_drop",   # 震荡→down→流量突降（3 跳）
            "min_hops": 2,
            "reported": True,
        },
    },
    {
        "id": "sim-011",
        "label": "端口 CRC 错误",
        "site": "pod-A",
        "equipment": "net.port",
        "fault_type": "crc_error",
        "timestamp": "2026-11-21T10:50:00Z",
        "raw_text": "端口 CRC 错误计数持续增长（光口误码）",
        "signature": "ALERT-PORT-CRC-ERROR",
        "entity": "tor-A1:P1",
        "expected": {
            "start_code": "net.port.crc_error",
            "reaches": "net.port.down",             # CRC 保护触发端口关闭
            "pending": ["crc_error_down_protection"],
            "reported": True,
        },
    },
    {
        "id": "sim-006",
        "label": "动环 PSU 故障（T30）",
        "site": "pod-A",
        "equipment": "power.psu",
        "fault_type": "fault",
        "timestamp": "2026-11-21T10:25:00Z",
        "raw_text": "动环 PSU 故障告警（T30）",
        "signature": "T30alertPsuFault",
        "entity": "dci-psu-1",         # 动环域占位实体（拓扑未同步，如实降级）
        "expected": {
            "start_code": "power.psu.fault",
            "knowledge_gap": True,   # 归一命中但无因果出边：知识空白如实展示
            "reported": False,
        },
    },
    {
        "id": "sim-007",
        "label": "硬盘介质错误（未登记）",
        "site": "unknown",
        "equipment": "storage.disk",
        "fault_type": "media_error",
        "timestamp": "2026-11-21T10:30:00Z",
        "raw_text": "硬盘介质错误（未登记告警类型）",
        "signature": "DISK-MEDIA-ERROR-UNREGISTERED",
        "expected": {
            "unmapped": True,        # 未登记：走人工登记路径，不编造
            "reported": False,
        },
    },
    {
        "id": "sim-008",
        "label": "机柜母线失电（风暴）",
        "site": "pod-A",
        "equipment": "power.busbar",
        "fault_type": "input_lost",
        "timestamp": "2026-11-21T10:35:00Z",
        "raw_text": "机柜母线输入失电（告警风暴场景）",
        "signature": "ALERT-BUSBAR-INPUT-LOST",
        "entity": "busbar-A",
        "expected": {
            "start_code": "power.rack.busbar.input_lost",
            "reaches": "net.port.down",
            "radius_entities": ["pdu-A1", "pdu-A2", "srv-001:p0", "tor-A1:E1", "cab-A12"],
            "reported": True,
        },
    },
]


def normalize(equipment: str, fault_type: str) -> str | None:
    """部位+类型 → 告警 signature；未登记返回 None（人工路径）。"""
    return ALERT_REGISTRY.get((equipment, fault_type))
