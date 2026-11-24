"""风暴诊断后对话可追问：上下文缓存覆盖两个判定序（正向+风暴）。

bug（2026-11-24 用户实测）：点告警风暴后对话拿不到上下文——storm 端点
没写 _LAST_DIAGNOSIS，chat 仍报"尚无诊断结果"。
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from diagnosis.api import app
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def client():
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），风暴上下文测试跳过")
    return TestClient(app)


def test_storm_populates_chat_context(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """跑一次风暴 → chat 不再报'尚无诊断'（改报 LLM 未配置——可区分）。"""
    from diagnosis import api as api_mod
    api_mod._LAST_DIAGNOSIS.clear()
    resp = client.post("/api/diagnosis/storm", json={"alerts": [
        {"entity": "srv-001:psu1", "stable_code": "server.psu.input_lost"},
        {"entity": "srv-002:psu1", "stable_code": "server.psu.input_lost"},
    ]})
    assert resp.status_code == 200
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)
    chat = client.post("/api/diagnosis/chat", json={"question": "根因是什么？"})
    assert chat.status_code == 422
    assert "LLM 未配置" in chat.json()["detail"], \
        f"应报 LLM 未配置（证明上下文已注入），实际: {chat.json()['detail']}"


def test_storm_context_builder():
    """风暴形态的上下文构建：根因/波及实体/验证结论全部注入。"""
    from diagnosis.tools.llm import build_diagnosis_context

    state = {
        "kind": "storm",
        "root_cause": {"entity": "pdu-A1", "stable_code": "power.rack.pdu.output_lost_dual",
                       "verify": "knowledge_backed"},
        "affected_entities": ["srv-001:psu1", "srv-002:psu1"],
        "independent": False,
        "verify": "knowledge_backed",
        "conclusion": {"summary": "疑似上层故障：pdu-A1", "rationale": "共同供电点"},
    }
    ctx = build_diagnosis_context(state)
    assert "pdu-A1" in ctx
    assert "power.rack.pdu.output_lost_dual" in ctx
    assert "srv-001:psu1" in ctx
    assert "疑似上层故障" in ctx
