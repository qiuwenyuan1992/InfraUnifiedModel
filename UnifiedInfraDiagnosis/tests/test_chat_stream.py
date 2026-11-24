"""诊断对话流式（SSE）：token/思考过程逐段推送。

用户反馈（2026-11-24）：对话一次性返回太慢、看不到思考过程。改为
SSE 流式：reasoning_delta（思考逐段）→ answer_delta（回答逐段）→
done（终态）。未配置 LLM 仍 422 降级（AGENTS：LLM 可降级纪律）。
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
        pytest.skip("OKB server 未运行（18090），流式对话测试跳过")
    return TestClient(app)


def _parse_sse(lines) -> list[tuple[str, dict]]:
    events: list[tuple[str, dict]] = []
    for l in lines:
        if l.startswith("event: "):
            events.append((l[7:], {}))
        elif l.startswith("data: ") and events:
            events[-1] = (events[-1][0], json.loads(l[6:]))
    return events


def test_chat_stream_unconfigured_422(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """未配置 LLM：422 降级（流式端点同样守纪律）。"""
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY", "OKB_LLM_MODEL"):
        monkeypatch.delenv(k, raising=False)
    resp = client.post("/api/diagnosis/chat/stream", json={"question": "为什么？"})
    assert resp.status_code == 422
    assert "未配置" in resp.json()["detail"]


def test_chat_stream_requires_diagnosis(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """无诊断结果：422（先诊断后追问）。"""
    monkeypatch.setenv("OKB_LLM_BASE_URL", "https://gw.example/v1")
    monkeypatch.setenv("OKB_LLM_API_KEY", "k")
    # 进程内缓存可能被同进程其他测试填充——显式清空再验证
    from diagnosis import api as api_mod
    api_mod._LAST_DIAGNOSIS.clear()
    resp = client.post("/api/diagnosis/chat/stream", json={"question": "为什么？"})
    assert resp.status_code == 422
    assert "诊断" in resp.json()["detail"]
