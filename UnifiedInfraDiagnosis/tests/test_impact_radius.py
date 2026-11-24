"""影响半径与告警风暴对账：因果图 × 拓扑 fixture 交替遍历（两张图合起来）。

验证涌现结论：busbar 失电的预期告警集（2 PDU + 4 服务器链路 + TOR 口 +
机柜超技术功耗）没有任何规则手写过，由 OKB 因果链与 fixture 拓扑遍历得出。
制冷域绑定不可用如实降级（等拓扑同步，不特判）。
OKB server 未运行时跳过——不伪造通过。
"""

from __future__ import annotations

import pytest

from diagnosis.graphs.impact import bind_entities, compute_impact_radius, reconcile_storm
from diagnosis.graphs.rack_power import diagnose
from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def okb() -> OkbClient:
    client = OkbClient()
    if not client.healthz():
        pytest.skip("OKB server 未运行（18090），影响半径测试跳过——不伪造通过")
    return client


@pytest.fixture(scope="module")
def busbar_radius(okb: OkbClient) -> dict:
    """busbar-A 失电的影响半径（trace 真库 + fixture 拓扑）。"""
    trace = okb.trace("power.rack.busbar.input_lost", max_hops=6, top_k=10, include_draft=True)
    return compute_impact_radius(trace, "busbar-A", FixtureInventory())


def test_power_radius_emergent(busbar_radius: dict):
    """涌现断言：供电树全量展开，预期告警覆盖 PDU/端口/机柜。"""
    alerts = {(a["entity"], a["stable_code"]) for a in busbar_radius["expected_alerts"]}
    # PDU 双路掉电预期（供电下游的 PDU 实体）
    assert ("pdu-A", "power.rack.pdu.output_lost_dual") in alerts
    assert ("pdu-B", "power.rack.pdu.output_lost_dual") in alerts
    # TOR 口掉 DOWN 预期（same_rack_tor_batch：受影响服务器链路两端）
    assert ("tor-A:E1", "net.port.down") in alerts
    assert ("srv-001:p0", "net.port.down") in alerts
    # 机柜超技术功耗预期（容器绑定）
    assert ("cab-A12", "power.rack.cabinet.over_tech") in alerts
    # 证据回链：每条预期告警带 edge_id
    assert all(a["edge_id"] for a in busbar_radius["expected_alerts"])


def test_storm_reconcile(busbar_radius: dict):
    """风暴对账：实际只来了 2 PDU + 1 个 TOR 口告警 → 缺失被点名。"""
    observed = [
        {"entity": "pdu-A", "stable_code": "power.rack.pdu.output_lost_dual"},
        {"entity": "pdu-B", "stable_code": "power.rack.pdu.output_lost_dual"},
        {"entity": "tor-A:E1", "stable_code": "net.port.down"},
    ]
    rec = reconcile_storm(busbar_radius, observed)
    assert ("pdu-A", "power.rack.pdu.output_lost_dual") in rec["matched"]
    # 3 个 TOR 口告警缺失：预期 4 只来 1 —— 缺失本身就是信号（如实点名）
    assert ("tor-A:E2", "net.port.down") in rec["missing"]
    assert len(rec["missing"]) >= 3
    assert rec["unexpected"] == []


def test_storm_reconcile_unexpected(busbar_radius: dict):
    """对账发现预期外告警：交根因诊断（另有故障的信号）。"""
    observed = [
        {"entity": "pdu-A", "stable_code": "power.rack.pdu.output_lost_dual"},
        {"entity": "srv-007", "stable_code": "server.psu.input_lost"},  # 不在预期集
    ]
    rec = reconcile_storm(busbar_radius, observed)
    assert ("srv-007", "server.psu.input_lost") in rec["unexpected"]


def test_cooling_binding_unavailable():
    """制冷域拓扑未同步：绑定不可用（None），不编造实体。"""
    assert bind_entities({"srv-001"}, "cooling.chiller.fault", FixtureInventory()) is None


def test_diagnose_with_entity(okb: OkbClient):
    """端到端：告警带实体 → 判定序产出影响半径（服务器 PSU 掉电 → 本机宕机预期）。"""
    state = diagnose("SERVER-PSU-INPUT-LOST", okb=okb, entity_id="srv-001")
    alerts = {(a["entity"], a["stable_code"]) for a in state["impact_radius"]["expected_alerts"]}
    assert ("srv-001", "server.sys.crash.unexpected") in alerts


def test_diagnose_without_entity_skips_radius(okb: OkbClient):
    """纯 signature 输入（无实体）：影响半径跳过，不报错——兼容既有调用。"""
    state = diagnose("SERVER-PSU-INPUT-LOST", okb=okb)
    assert state["impact_radius"] == {}
