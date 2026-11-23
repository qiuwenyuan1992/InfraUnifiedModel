"""掉电判定序金路径回归测试。

直连真实 OKB server（18090）：本测试验证的是"诊断编排消费真库种子数据"的端到端行为，
种子由 seeder 保证（T30alertPsuFault → power.psu.fault，同事件对端 rack PDU 掉电）。
OKB server 未运行时跳过（不伪造通过）。
"""

from __future__ import annotations

import pytest

from diagnosis.graphs.rack_power import diagnose
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def okb() -> OkbClient:
    client = OkbClient()
    if not client.healthz():
        pytest.skip("OKB server 未运行（18090），金路径测试跳过——不伪造通过")
    return client


def test_power_outage_golden_path(okb: OkbClient):
    """金路径：服务器掉电告警 → 归一 input_lost → trace 至宕机 → 同事件对端取证 → 回写。"""
    state = diagnose("SERVER-PSU-INPUT-LOST", okb=okb)

    # 归一命中
    assert state["candidates"], f"lookup 未命中: {state.get('error')}"
    cand = state["candidates"][0]
    assert cand["stable_code"] == "server.psu.input_lost"

    # 传播推演：因果链推进到宕机（条件 psu_redundancy 未知 → 作为待验证检查项）
    chains = state["trace_result"].get("chains", [])
    assert chains, "trace 必须返回候选链"
    crash_hops = [
        h for c in chains
        for h in c["hops"] if h["target_stable_code"] == "server.sys.crash.unexpected"
    ]
    assert crash_hops, "掉电链必须推进到宕机节点"

    # unknown 条件作为待验证检查项（evidence-driven：Agent 去查冗余是否已失）
    assert state["pending_checks"], "psu_redundancy 条件未知必须成为待验证检查项"
    assert any(pc["fact_key"] == "psu_redundancy" for pc in state["pending_checks"])

    # 跨域取证提示：same_event 对端 = 同柜 PDU 双路掉电
    peers = state["cross_domain_peers"]
    assert any(p["other_code"] == "power.rack.pdu.output_lost_dual" for p in peers), \
        f"必须提示同柜 PDU 掉电对端: {peers}"

    # 结论带证据回链与回写完成
    conclusion = state["conclusion"]
    assert conclusion["status"] == "candidate_chains"
    assert conclusion["start_code"] == "server.psu.input_lost"
    assert conclusion["primary_chain"]
    assert state["reported"] is True


def test_t30_psu_fault_knowledge_gap(okb: OkbClient):
    """知识空白路径如实断言：T30 告警可归一（power.psu.fault），但该节点无因果出边——
    传播推演为空时结论必须暴露知识空白，不得编造链。"""
    state = diagnose("T30alertPsuFault", okb=okb)
    assert state["candidates"], "T30 必须归一命中 power.psu.fault"
    assert state["candidates"][0]["stable_code"] == "power.psu.fault"
    # 无因果出边：trace 链为空，结论如实体現知识空白
    conclusion = state["conclusion"]
    assert conclusion["primary_chain"] == [], "无出边节点不得编造传播链"
    assert state["reported"] is False


def test_unknown_alert_unmapped(okb: OkbClient):
    """未登记告警：不编造候选，明确 unmapped 状态走人工路径。"""
    state = diagnose("NoSuchAlert-Signature", okb=okb)
    assert state["error"], "未登记告警必须产生 error 说明"
    assert state["conclusion"]["status"] == "unmapped_alert"
    assert state["reported"] is False
