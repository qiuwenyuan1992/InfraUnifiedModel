"""探索式诊断：Agent 用工具查数据（OKB/Inventory 抽象），过程可视化。

设计（2026-11-24 用户确认方向）：
- Agent 的每次工具调用都变成前端拓扑上的动作（点亮/取证/剪枝）——
  "大模型怎么查数据"从黑盒变成可观测序列
- 工具层就是现有抽象（OkbClient 四原语 + InventoryPort）：未来对接
  真实数据源零改动（换实现不换接口）
- 验证循环：trace 遇 pending → verify_fact 从**拓扑**取证回填 →
  带 facts 重 trace → 条件不满足的边被剪枝（真库实测：
  psu_redundancy=ok → chains 归零）
- LLM 可选：配置了网关走 LLM agent（OpenAI tools 协议），未配置
  走规则 agent（固定序 + 相同事件流）——降级纪律
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
        pytest.skip("OKB server 未运行（18090），探索式诊断测试跳过")
    return TestClient(app)


def _run_explore(client: TestClient, signature: str, entity: str,
                 monkeypatch: pytest.MonkeyPatch | None = None) -> list[tuple[str, dict]]:
    """跑探索（默认规则路径——清 LLM 环境防 .env 真网关介入）。"""
    if monkeypatch is not None:
        for k in ("OKB_LLM_BASE_URL", "OKB_LLM_API_KEY"):
            monkeypatch.delenv(k, raising=False)
    events: list[tuple[str, dict]] = []
    with client.stream("POST", "/api/diagnosis/explore", json={
        "alert_signature": signature, "entity_id": entity,
    }) as resp:
        assert resp.status_code == 200
        for line in resp.iter_lines():
            if line.startswith("event: "):
                events.append((line[7:], None))
            elif line.startswith("data: ") and events and events[-1][1] is None:
                events[-1] = (events[-1][0], json.loads(line[6:]))
    return events


def test_explore_streams_incrementally(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """真流式：step 帧在推理完成前送达（非跑完一次性吐帧）。

    假 run_explore 发首步后阻塞等放行——若端点缓冲，读首帧要等满
    block 超时（5s）；真流式应在 1s 内读到。与 AG-UI 端点同构守卫。
    """
    import threading
    import time

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
    with client.stream("POST", "/api/diagnosis/explore", json={
        "alert_signature": "X-SIG", "entity_id": "",
    }) as resp:
        lines = resp.iter_lines()
        first = next(l for l in lines if l.startswith("event: "))
        latency = time.monotonic() - t0
        block.set()  # 收到首帧，放行剩余
        rest = [l for l in lines if l.startswith("event: ")]
    assert first == "event: step", f"首帧应为 step: {first}"
    assert latency < 3.0, f"首帧延迟 {latency:.2f}s——端点在缓冲（跑完才吐帧）"
    assert any("done" in l for l in rest), "放行后应收到余帧"


def test_explore_rule_sequence_and_prune(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """PSU 场景完整验证循环：查→推演→取证→回填→剪枝→结论。"""
    events = _run_explore(client, "SERVER-PSU-INPUT-LOST", "srv-001", monkeypatch)
    tools = [d["tool"] for ev, d in events if ev == "step"]
    # 规则降级路径的固定序（LLM 未配置或失败时）
    assert tools[0] == "lookup_alert"
    assert tools[1] == "trace_causal"
    assert "verify_fact" in tools, "遇到 pending 必须取证"
    assert tools.count("trace_causal") >= 2, "事实回填后必须重推演"
    assert tools[-1] == "submit_conclusion"

    # 取证步：verify_fact 从拓扑解析冗余（两张图合起来的落点）
    vf = [d for ev, d in events if ev == "step" and d["tool"] == "verify_fact"][0]
    assert vf["result"]["fact_key"] == "psu_redundancy"
    assert vf["result"]["value"] == "ok"
    assert vf["result"]["evidence_entities"], "取证必须带证据实体（拓扑上点亮）"

    # 剪枝：第二次 trace 后宕机跳被剪掉
    done = [d for ev, d in events if ev == "done"][0]
    assert "server.sys.crash.unexpected" in done["pruned"], "psu_redundancy=ok 后宕机分支应被剪枝"
    assert done["facts"] == {"psu_redundancy": "ok"}
    assert done["conclusion"]["summary"], "必须有结论"


def test_explore_steps_carry_topo_actions(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """每步带 topo 动作（前端可视化数据源）：点亮/取证实体。"""
    events = _run_explore(client, "SERVER-PSU-INPUT-LOST", "srv-001", monkeypatch)
    steps = [d for ev, d in events if ev == "step"]
    for s in steps:
        assert "topo" in s, f"步 {s['tool']} 必须带 topo 动作"
    # 首步点亮告警实体；取证步带证据实体
    assert "srv-001" in (steps[0]["topo"].get("examined") or [])
    vf = [s for s in steps if s["tool"] == "verify_fact"][0]
    assert vf["topo"].get("evidence")


def test_explore_no_pending_skips_verify(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """无 pending 场景（BGP 叶子）：无取证步，链为空如实结论。"""
    events = _run_explore(client, "ALERT-BGP-PEER-DOWN", "bgp-1", monkeypatch)
    tools = [d["tool"] for ev, d in events if ev == "step"]
    assert "verify_fact" not in tools
    done = [d for ev, d in events if ev == "done"][0]
    assert done["pruned"] == []


def test_explore_unmapped_alert(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    """未登记告警：lookup 失败如实终止，不编造。"""
    events = _run_explore(client, "NOT-EXISTENT-SIG", "srv-001", monkeypatch)
    done = [d for ev, d in events if ev == "done"][0]
    error = [d for ev, d in events if ev == "error"][0]
    assert error["message"]


def test_llm_agent_tool_loop(monkeypatch: pytest.MonkeyPatch):
    """LLM agent：tools 协议回路（fake 网关两轮：先 tool_call 后终答）。"""
    import httpx

    from diagnosis.graphs.explore import TOOL_SPECS, run_explore

    calls: list[dict] = []

    class FakeResp:
        def raise_for_status(self): pass
        def json(self):
            if len(calls) == 1:  # 第一轮：LLM 决定查 trace
                return {"choices": [{"message": {"role": "assistant", "content": "",
                    "tool_calls": [{"id": "c1", "type": "function",
                        "function": {"name": "lookup_alert", "arguments": "{\"signature\": \"SERVER-PSU-INPUT-LOST\"}"}}]}}]}
            return {"choices": [{"message": {"role": "assistant", "content": "结论：已归一"}}]}  # 终答

    def fake_post(url, headers=None, json=None, timeout=None):
        calls.append(json)
        return FakeResp()

    monkeypatch.setattr(httpx, "post", fake_post)
    monkeypatch.setenv("OKB_LLM_BASE_URL", "https://gw.example/v1")
    monkeypatch.setenv("OKB_LLM_API_KEY", "k")

    from diagnosis.tools.llm import LlmClient
    steps: list[dict] = []
    done: dict = {}

    def emit(ev: str, payload: dict):
        if ev == "step":
            steps.append(payload)
        elif ev == "done":
            done.update(payload)

    result = run_explore("SERVER-PSU-INPUT-LOST", "srv-001", OkbClient(),
                         LlmClient(), emit=emit)
    # 请求体带 tools 规格与消息历史（OpenAI tools 协议）
    assert calls[0]["tools"] == TOOL_SPECS
    assert calls[1]["messages"][-1]["role"] == "tool", "工具结果必须回填消息历史"
    # 工具步被发出 + 终答作为结论
    assert steps[0]["tool"] == "lookup_alert"
    assert "已归一" in result["conclusion"]["summary"]
