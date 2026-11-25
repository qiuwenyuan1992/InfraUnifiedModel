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


def test_agui_run_ids_consistent(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """thread_id/run_id 回传客户端会话语义（RunStarted/Finished 一致）。"""
    events = _run_agui(client, monkeypatch)
    run_ids = {e["runId"] for e in events if "runId" in e}
    thread_ids = {e["threadId"] for e in events if "threadId" in e}
    assert run_ids == {"r-test"}, f"run_id 必须回传: {run_ids}"
    assert thread_ids == {"t-test"}
