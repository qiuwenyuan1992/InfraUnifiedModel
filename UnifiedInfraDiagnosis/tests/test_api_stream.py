"""诊断 API 流式接口测试：SSE 事件序列与终态断言（TestClient，OKB 未运行时跳过）。"""

from __future__ import annotations

import json

import pytest

from diagnosis.api import app
from diagnosis.tools.okb import OkbClient

fastapi = pytest.importorskip("fastapi")
from fastapi.testclient import TestClient  # noqa: E402


@pytest.fixture(scope="module")
def client() -> TestClient:
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），API 测试跳过——不伪造通过")
    return TestClient(app)


def test_sse_golden_path(client: TestClient):
    """金路径 SSE：node_update 五节点依序推送，done 带完整结论。"""
    with client.stream(
        "POST", "/api/diagnosis/run", json={"alert_signature": "SERVER-PSU-INPUT-LOST"}
    ) as resp:
        assert resp.status_code == 200
        events = _parse_sse(resp.iter_lines())
    nodes = [e["payload"]["node"] for e in events if e["event"] == "node_update"]
    # 判定序五节点依序执行
    assert nodes == [
        "normalize_alert", "propagate_trace",
        "cross_domain_hints", "conclude", "report_feedback",
    ]
    done = [e["payload"] for e in events if e["event"] == "done"][0]
    assert done["conclusion"]["start_code"] == "server.psu.input_lost"
    assert done["conclusion"]["primary_chain"], "主链必须带证据回链"
    assert done["reported"] is True
    assert any(p["other_code"] == "power.rack.pdu.output_lost_dual"
               for p in done["cross_domain_peers"])


def test_sse_unknown_alert(client: TestClient):
    """未登记告警：done 带空结论与 error 说明，不编造链。"""
    with client.stream(
        "POST", "/api/diagnosis/run", json={"alert_signature": "NoSuchAlert"}
    ) as resp:
        events = _parse_sse(resp.iter_lines())
    done = [e["payload"] for e in events if e["event"] == "done"][0]
    assert done["error"], "未登记告警必须带 error 说明"
    assert done["conclusion"].get("status") == "unmapped_alert"
    assert done["conclusion"].get("primary_chain", []) == []
    assert done["reported"] is False


def test_healthz(client: TestClient):
    resp = client.get("/api/diagnosis/healthz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"
    assert resp.json()["okb"] is True


def _parse_sse(lines) -> list[dict]:
    """解析 SSE 帧：event:/data: 成对组装。"""
    events, cur_event, cur_data = [], None, []
    for line in lines:
        if isinstance(line, bytes):
            line = line.decode("utf-8")
        if line.startswith("event: "):
            cur_event = line[len("event: "):]
        elif line.startswith("data: "):
            cur_data.append(line[len("data: "):])
        elif line == "" and cur_event:
            events.append({"event": cur_event, "payload": json.loads("\n".join(cur_data))})
            cur_event, cur_data = None, []
    return events
