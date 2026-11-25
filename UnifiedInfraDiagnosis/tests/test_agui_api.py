"""AG-UI 协议端点：探索式诊断的标准化事件流（新页面验证效果）。

协议（2026-11-24 用户要求新页面看 AG-UI 效果）：ag-ui-protocol 1.0
事件替换手搓帧——RunStarted → ToolCall*（每个工具步）+ Custom（拓扑动作）
→ TextMessage*（结论）→ RunFinished。LLM 模式另发 Reasoning*。
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
        pytest.skip("OKB server 未运行（18090），AG-UI 端点测试跳过")
    return TestClient(app)


def _parse_sse(lines) -> list[dict]:
    """解析 data: 行为 dict 列表（AG-UI 每帧一个 JSON 事件）。"""
    return [json.loads(l[6:]) for l in lines if l.startswith("data: ")]


def _run_agui(client: TestClient, monkeypatch: pytest.MonkeyPatch | None = None):
    """标准 RunAgentInput 载荷跑 AG-UI 端点（默认规则模式）。"""
    if monkeypatch is not None:
        for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
            monkeypatch.delenv(k, raising=False)
    with client.stream("POST", "/api/diagnosis/agui", json={
        "threadId": "t-test", "runId": "r-test",
        "state": {"alert_signature": "SERVER-PSU-INPUT-LOST", "entity_id": "srv-001"},
        "messages": [{"id": "m1", "role": "user", "content": "SERVER-PSU-INPUT-LOST"}],
    }) as resp:
        assert resp.status_code == 200
        assert "text/event-stream" in resp.headers["content-type"]
        return _parse_sse(resp.iter_lines())


def test_agui_event_sequence(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """规则模式完整事件序：RunStarted → ToolCall 四件套×N → TextMessage 三件套 → RunFinished。"""
    events = _run_agui(client, monkeypatch)

    types = [e["type"] for e in events]
    assert types[0] == "RUN_STARTED"
    assert types[-1] == "RUN_FINISHED", f"必须以 RunFinished 收尾: {types[-3:]}"

    # 工具步：AG-UI ToolCall 四件套（Start/Args/End/Result）
    tool_names = [e["toolCallName"] for e in events if e["type"] == "TOOL_CALL_START"]
    assert tool_names[0] == "lookup_alert"
    assert "verify_fact" in tool_names
    assert tool_names[-1] == "submit_conclusion"
    for t in ("TOOL_CALL_START", "TOOL_CALL_ARGS", "TOOL_CALL_END", "TOOL_CALL_RESULT"):
        assert t in types, f"缺 {t}"

    # 结论：TextMessage 三件套
    assert "TEXT_MESSAGE_START" in types
    assert "TEXT_MESSAGE_CONTENT" in types
    assert "TEXT_MESSAGE_END" in types
    content = "".join(e["delta"] for e in events if e["type"] == "TEXT_MESSAGE_CONTENT")
    assert "server.psu.input_lost" in content

    # 拓扑动作：CUSTOM 事件（前端点亮数据源）
    customs = [e for e in events if e["type"] == "CUSTOM"]
    assert any(e["name"] == "topo_action" for e in customs), "拓扑动作必须走 CUSTOM 事件"

    # 剪枝信息：CUSTOM（pruned）
    pruned_ev = [e for e in customs if e["name"] == "pruned"]
    assert pruned_ev and "server.sys.crash.unexpected" in pruned_ev[0]["value"]


def test_agui_conclusion_graph(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """结论拓扑：CUSTOM conclusion_graph 携带结构化传播链（code/chain/pruned/facts）。

    用母线失电场景（双 PDU 全失 → 宕机分支无冗余可剪，存活链非空）；
    PSU 场景单跳全剪属"无级联"结论，另断言。
    """
    with client.stream("POST", "/api/diagnosis/agui", json={
        "threadId": "t-cg", "runId": "r-cg",
        "state": {"alert_signature": "ALERT-BUSBAR-INPUT-LOST", "entity_id": "busbar-A"},
    }) as resp:
        events = _parse_sse(resp.iter_lines())
    cg = [e for e in events if e["type"] == "CUSTOM" and e["name"] == "conclusion_graph"]
    assert cg, "必须发 conclusion_graph CUSTOM 事件"
    v = cg[0]["value"]
    assert v["code"] == "power.rack.busbar.input_lost", "起始异常码"
    surviving = [h for h in v["chain"] if h not in v["pruned"]]
    assert surviving, f"母线失电应有存活传播链: {v}"
    assert v["facts"], "取证事实"

    # PSU 场景：冗余正常 → 全链剪除（无级联结论）
    events = _run_agui(client, monkeypatch)
    v2 = [e for e in events if e["type"] == "CUSTOM" and e["name"] == "conclusion_graph"][0]["value"]
    assert v2["code"] == "server.psu.input_lost"
    assert "server.sys.crash.unexpected" in v2["pruned"]
    assert [h for h in v2["chain"] if h not in v2["pruned"]] == [], "单跳全剪=无级联"
    assert v2["facts"].get("psu_redundancy") == "ok"


def test_agui_run_ids_consistent(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """thread_id/run_id 回传客户端会话语义（RunStarted/Finished 一致）。"""
    events = _run_agui(client, monkeypatch)
    run_ids = {e["runId"] for e in events if "runId" in e}
    thread_ids = {e["threadId"] for e in events if "threadId" in e}
    assert run_ids == {"r-test"}, f"run_id 必须回传: {run_ids}"
    assert thread_ids == {"t-test"}


def test_agui_streams_incrementally(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """真流式：首帧在推理完成前送达（非跑完一次性吐帧）。

    假 run_explore 发首步后阻塞等放行——若端点缓冲，读首帧要等满
    block 超时（5s）；真流式应在 1s 内读到。时间断言裕量足够大。
    """
    import threading
    import time

    from diagnosis import api as api_mod
    from diagnosis.graphs import explore as explore_mod

    block = threading.Event()

    def fake_explore(signature, entity_id, okb, llm, emit):
        emit("step", {"index": 1, "tool": "lookup_alert",
                      "args": {"signature": signature}, "result": {"candidates": []},
                      "thought": "", "topo": {}})
        block.wait(timeout=5.0)  # 阻塞：首帧送达后才放行
        return {"conclusion": {"summary": "完"}, "pruned": [], "facts": {}}

    monkeypatch.setattr(explore_mod, "run_explore", fake_explore)
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)

    t0 = time.monotonic()
    with client.stream("POST", "/api/diagnosis/agui", json={
        "threadId": "t-live", "runId": "r-live",
        "state": {"alert_signature": "X-SIG", "entity_id": ""},
    }) as resp:
        lines = resp.iter_lines()
        first = next(l for l in lines if l.startswith("data: "))
        latency = time.monotonic() - t0
        block.set()  # 收到首帧，放行剩余
        rest = [l for l in lines if l.startswith("data: ")]
    assert "RUN_STARTED" in first, f"首帧应为 RunStarted: {first}"
    assert latency < 3.0, f"首帧延迟 {latency:.2f}s——端点在缓冲（跑完才吐帧）"
    assert any("RUN_FINISHED" in l for l in rest), "放行后应收到余帧"
