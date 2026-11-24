"""风暴根因反推收编 LangGraph（复核偏差③）。

此前 infer_power_root 是游离 REST 端点：不在图里——无节点流、不产
pending_checks、与正向诊断两套呈现。收编后：风暴判定序 =
ingest_alerts → infer_common_ancestor → verify_against_okb → conclude，
端点 /api/diagnosis/storm 走图执行（结果结构与既有页面字段兼容）。
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from diagnosis.graphs.storm_graph import build_storm_graph, init_storm_state
from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def okb() -> OkbClient:
    client = OkbClient()
    if not client.healthz():
        pytest.skip("OKB server 未运行（18090），风暴图测试跳过")
    return client


def _alerts(n: int = 6, side: str = "psu1") -> list[dict]:
    return [{"entity": f"srv-{i:03d}:{side}", "stable_code": "server.psu.input_lost"}
            for i in range(1, n + 1)]


def test_storm_graph_is_langgraph(okb: OkbClient):
    """风暴判定序是 LangGraph StateGraph（非游离函数）。"""
    graph = build_storm_graph(okb)
    assert graph.nodes, "图必须装配出节点"
    result = graph.invoke(init_storm_state(_alerts()))
    # 与原 infer_power_root 同判定：PDU 根因 + OKB 验证
    assert result["root_cause"]["entity"] == "pdu-A1"
    assert result["root_cause"]["verify"] == "knowledge_backed"
    assert result["conclusion"]["summary"]


def test_storm_graph_nodes_sequence(okb: OkbClient):
    """节点依序执行且可流式（stream_mode=updates）——与正向诊断同机制。"""
    graph = build_storm_graph(okb)
    updates = list(graph.stream(init_storm_state(_alerts()), stream_mode="updates"))
    names = [n for u in updates for n in u]
    assert names == ["ingest_alerts", "infer_common_ancestor", "verify_against_okb", "conclude"]


def test_storm_graph_independent_case(okb: OkbClient):
    """跨柜独立告警：图如实拒绝反推（不硬凑根因）。"""
    graph = build_storm_graph(okb)
    state = init_storm_state([
        {"entity": "srv-001:psu1", "stable_code": "server.psu.input_lost"},
        {"entity": "srv-101:psu1", "stable_code": "server.psu.input_lost"},
    ])
    result = graph.invoke(state)
    assert result["root_cause"] is None
    assert "无共同供电点" in result["conclusion"]["rationale"]


def test_storm_api_uses_graph(okb: OkbClient):
    """端点契约（SSE）：node_update 四节点依序 + done 带根因结果。"""
    import json

    from diagnosis.api import app

    with TestClient(app) as client:
        with client.stream("POST", "/api/diagnosis/storm", json={"alerts": _alerts(3)}) as resp:
            assert resp.status_code == 200
            events = []
            for line in resp.iter_lines():
                if line.startswith("event: "):
                    events.append([line[7:], None])
                elif line.startswith("data: ") and events and events[-1][1] is None:
                    events[-1][1] = json.loads(line[6:])
        nodes = [e[1]["node"] for e in events if e[0] == "node_update"]
        assert nodes == ["ingest_alerts", "infer_common_ancestor", "verify_against_okb", "conclude"]
        done = [e[1] for e in events if e[0] == "done"][0]
        assert done["root_cause"]["entity"] == "pdu-A1"
        assert done["alert_count"] == 3
