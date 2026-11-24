"""诊断对话 API：LLM 座位第一刀（拿诊断终态回答追问），可降级。

- POST /api/diagnosis/chat {question, history?}：对**最近一次诊断**追问；
  服务端缓存最近诊断终态（进程内存，测试页够用；会话化待 checkpointer）。
- LLM 未配置 → 422 带降级说明（前端如实提示"未配置 LLM"）。
- 证据纪律：上下文由 build_diagnosis_context 注入（结论/链/待验证/半径），
  LLM 不自由遍历图。
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from diagnosis.api import app
from diagnosis.tools.llm import LlmClient, build_diagnosis_context, SYSTEM_PROMPT


@pytest.fixture(scope="module")
def client():
    from diagnosis.tools.okb import OkbClient
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），对话 API 测试跳过")
    return TestClient(app)


def test_chat_unconfigured_llm_degrades(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """LLM 未配置：422 + 如实降级说明（不伪装成模型回答）。"""
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY", "OKB_LLM_MODEL"):
        monkeypatch.delenv(k, raising=False)  # 开发者本机可能配了 .env
    resp = client.post("/api/diagnosis/chat", json={"question": "为什么判定会宕机？"})
    assert resp.status_code == 422
    assert "未配置" in resp.json()["detail"]


def test_chat_context_builder_contains_evidence():
    """上下文构建：诊断终态的关键字段全部注入（LLM 只见这些，不越界）。"""
    state = {
        "alert_signature": "SERVER-PSU-INPUT-LOST", "entity_id": "srv-001",
        "start_code": "server.psu.input_lost",
        "conclusion": {"summary": "掉电", "primary_chain": [{"target": "server.sys.crash.unexpected",
                                                                "evidence_level": "curated", "revision_id": "r1"}]},
        "pending_checks": [{"fact_key": "psu_redundancy", "op": "eq", "edge_id": "e1"}],
        "cross_domain_peers": [{"other_code": "power.rack.pdu.output_lost_dual"}],
        "impact_radius": {"expected_alerts": [{"entity": "srv-001", "stable_code": "server.sys.crash.unexpected",
                                               "edge_id": "e1", "revision_id": "r1"}],
                          "unbound": []},
    }
    ctx = build_diagnosis_context(state)
    assert "server.psu.input_lost" in ctx
    assert "server.sys.crash.unexpected" in ctx
    assert "psu_redundancy" in ctx
    assert "srv-001" in ctx
    assert "掉电" in ctx
    assert "curated" in ctx, "主链各跳的证据等级必须注入（LLM 要能引用等级）"


def test_chat_returns_reasoning(monkeypatch: pytest.MonkeyPatch):
    """思考模型的 reasoning_content 透传（前端折叠展示）。"""
    import httpx

    class FakeResp:
        def raise_for_status(self) -> None: pass
        def json(self) -> dict:
            return {"choices": [{"message": {"content": "答", "reasoning_content": "思考中…"}}]}

    captured: dict = {}

    def fake_post(url: str, headers: dict = None, json: dict = None, timeout=None):
        captured.update(url=url, body=json)
        return FakeResp()

    monkeypatch.setattr(httpx, "post", fake_post)
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("OKB_LLM_BASE_URL", "https://gw.example/v1")
    monkeypatch.setenv("OKB_LLM_API_KEY", "k")
    client = LlmClient()
    result = client.chat("sys", "问")
    assert result["content"] == "答"
    assert result["reasoning"] == "思考中…"
    assert captured["url"] == "https://gw.example/v1/chat/completions"
    assert captured["body"]["messages"][0]["role"] == "system"


def test_langgraph_mermaid_endpoint(client: TestClient):
    """LangGraph 运行图：mermaid 语法 + 节点清单（可视化数据源）。"""
    resp = client.get("/api/diagnosis/graph", params={"which": "judge"})
    assert resp.status_code == 200
    body = resp.json()
    assert "normalize_alert" in body["nodes"]
    assert "report_feedback" in body["nodes"]
    assert body["mermaid"], "mermaid 语法必须生成"
    resp2 = client.get("/api/diagnosis/graph", params={"which": "storm"})
    assert "infer_common_ancestor" in resp2.json()["nodes"]


def test_system_prompt_has_evidence_discipline():
    """系统提示词必须含证据纪律（不可编造是命根）。"""
    assert "禁止编造" in SYSTEM_PROMPT
    assert "诊断上下文" in SYSTEM_PROMPT


def test_llm_client_config_gating(monkeypatch: pytest.MonkeyPatch):
    """无配置 available=False（降级路径的开关）。"""
    for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
        monkeypatch.delenv(k, raising=False)
    c = LlmClient()
    assert not c.available()
    monkeypatch.setenv("OKB_LLM_BASE_URL", "https://gw.example/v1")
    monkeypatch.setenv("OKB_LLM_API_KEY", "k")
    assert LlmClient().available()
