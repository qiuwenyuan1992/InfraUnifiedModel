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


def test_chat_unconfigured_llm_degrades(client: TestClient):
    """LLM 未配置：422 + 如实降级说明（不伪装成模型回答）。"""
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


def test_system_prompt_has_evidence_discipline():
    """系统提示词必须含证据纪律（不可编造是命根）。"""
    assert "禁止编造" in SYSTEM_PROMPT
    assert "诊断上下文" in SYSTEM_PROMPT


def test_llm_client_config_gating():
    """无配置 available=False（降级路径的开关）。"""
    c = LlmClient()
    # 测试环境无 OKB_LLM_BASE_URL/KEY（若开发者本机设了则跳过该断言）
    import os
    if os.environ.get("OKB_LLM_BASE_URL") and os.environ.get("OKB_LLM_API_KEY"):
        assert c.available()
    else:
        assert not c.available()
