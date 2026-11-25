"""合图（unified_graph）契约测试：一张图两种输入形态，行为与拆分版一致。

重构动机（README 待办置顶）：judge/storm 拆两张图违背"场景差异住 OKB
不住流程"——单条 vs 多条告警是输入形态差异。目标形态：

    ingest（条件路由：按告警条数）
      ├─ 1 条 → normalize → trace → hints → radius → conclude → report
      └─ N 条 → group → ancestor → verify → conclude → report

explore agent 同图跑法：工具调用变图节点，事件流（step 帧）不变。
"""

from __future__ import annotations

import json

import pytest
from fastapi.testclient import TestClient

from diagnosis.api import app
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def client():
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），合图测试跳过")
    return TestClient(app)


def _stream(client: TestClient, path: str, payload: dict) -> list[tuple[str, dict]]:
    events: list[tuple[str, dict]] = []
    with client.stream("POST", path, json=payload) as resp:
        assert resp.status_code == 200, f"{path} HTTP {resp.status_code}"
        for line in resp.iter_lines():
            if line.startswith("event: "):
                events.append((line[7:], None))
            elif line.startswith("data: ") and events and events[-1][1] is None:
                events[-1] = (events[-1][0], json.loads(line[6:]))
    return events


def test_unified_single_alert_takes_judge_branch(client: TestClient):
    """1 条告警 → 正向分支节点序 + done 契约（与拆分版 /run 一致）。"""
    events = _stream(client, "/api/diagnosis/run", {
        "alert_signature": "SERVER-PSU-INPUT-LOST", "entity_id": "srv-001"})
    nodes = [d["node"] for ev, d in events if ev == "node_update"]
    # 正向分支节点序（合图后子图序不变）
    for n in ("normalize_alert", "propagate_trace", "cross_domain_hints",
              "impact_radius", "conclude", "report_feedback"):
        assert n in nodes, f"正向分支缺 {n}: {nodes}"
    done = [d for ev, d in events if ev == "done"][0]
    assert done["conclusion"]["start_code"] == "server.psu.input_lost"
    assert done["impact_radius"]["expected_alerts"], "带实体的正向诊断应有影响半径"


def test_unified_storm_takes_storm_branch(client: TestClient):
    """N 条告警 → 风暴分支节点序 + done 带根因（与拆分版 /storm 一致）。"""
    events = _stream(client, "/api/diagnosis/storm", {"alerts": [
        {"entity": f"srv-{i:03d}:psu1", "stable_code": "server.psu.input_lost"}
        for i in range(1, 7)]})
    nodes = [d["node"] for ev, d in events if ev == "node_update"]
    for n in ("ingest_alerts", "infer_common_ancestor", "verify_against_okb", "conclude"):
        assert n in nodes, f"风暴分支缺 {n}: {nodes}"
    done = [d for ev, d in events if ev == "done"][0]
    assert done["root_cause"]["entity"] == "pdu-A1"
    assert done["alert_count"] == 6


def test_explore_graph_preserves_step_stream(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """探索式：工具调用改图节点后 step 事件流不变（前端零改动的保证）。"""
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)  # 规则降级路径
    events = _stream(client, "/api/diagnosis/explore", {
        "alert_signature": "SERVER-PSU-INPUT-LOST", "entity_id": "srv-001"})
    tools = [d["tool"] for ev, d in events if ev == "step"]
    assert tools[0] == "lookup_alert"
    assert "verify_fact" in tools
    assert tools[-1] == "submit_conclusion"
    done = [d for ev, d in events if ev == "done"][0]
    assert "server.sys.crash.unexpected" in done["pruned"]
    assert done["facts"]["psu_redundancy"] == "ok"


def test_explore_llm_loop_unchanged(monkeypatch: pytest.MonkeyPatch):
    """LLM agent 回路行为不变：tools 协议消息历史 + 步骤事件（fake 网关）。"""
    import httpx

    from diagnosis.graphs.explore import TOOL_SPECS, run_explore
    from diagnosis.tools.llm import LlmClient

    calls: list[dict] = []

    class FakeResp:
        def raise_for_status(self): pass
        def json(self):
            if len(calls) == 1:
                return {"choices": [{"message": {"role": "assistant", "content": "",
                    "tool_calls": [{"id": "c1", "type": "function",
                        "function": {"name": "lookup_alert",
                                     "arguments": "{\"signature\": \"SERVER-PSU-INPUT-LOST\"}"}}]}}]}
            return {"choices": [{"message": {"role": "assistant", "content": "结论：已归一"}}]}

    monkeypatch.setattr(httpx, "post", lambda url, headers=None, json=None, timeout=None:
                        (calls.append(json) or FakeResp()))
    monkeypatch.setenv("OKB_LLM_BASE_URL", "https://gw.example/v1")
    monkeypatch.setenv("OKB_LLM_API_KEY", "k")

    steps: list[dict] = []
    result = run_explore("SERVER-PSU-INPUT-LOST", "srv-001", OkbClient(),
                         LlmClient(), emit=lambda ev, p: steps.append(p) if ev == "step" else None)
    assert calls[0]["tools"] == TOOL_SPECS
    assert steps[0]["tool"] == "lookup_alert"
    assert "已归一" in result["conclusion"]["summary"]
