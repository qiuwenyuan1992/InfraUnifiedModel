"""飞轮回写守卫（生产语义，stub 客户端——不碰真库）。

复核发现的偏差①：report 节点此前无条件回写 confirmed——测试反复跑把
宕机边喂成 curated（飞轮吃测试数据）。守卫语义：
- pending_checks 未清零（判定未验证）→ 不回写
- report_enabled=False（测试/演示）→ 不回写
- 无待验证项 + 回写开启 → 回写 confirmed
"""

from __future__ import annotations

from diagnosis.graphs.judge_graph import build_graph, init_state
from diagnosis.tools.okb import OkbClient


class StubOkb(OkbClient):
    """记录 report 调用的 stub：其余原语返回固定结构。"""

    def __init__(self, pending: bool):
        self.reported_edges: list[str] = []
        self._pending = pending

    def healthz(self) -> bool:
        return True

    def lookup(self, signature: str, include_draft: bool = True) -> dict:
        return {"candidates": [{"anomaly_id": 1, "stable_code": "x.fault", "name": "x",
                                "status": "draft", "confidence": 0.9}]}

    def trace(self, code: str, max_hops: int = 6, top_k: int = 10,
              include_draft: bool = True, facts: dict | None = None) -> dict:
        hops = [{"target_stable_code": "x.down", "edge_id": "edge-1", "revision_id": "r1",
                 "evidence_level": "curated"}]
        evaluations = [{"fact_key": "cond", "op": "eq", "result": "unknown" if self._pending else "true"}]
        if self._pending:
            hops[0]["evaluations"] = evaluations
        return {"chains": [{"hops": hops}], "relations": []}

    def report(self, diagnosis_ref: str, edge_id: str, verdict: str, notes: str = "") -> dict:
        self.reported_edges.append(edge_id)
        return {"ok": True}


def _run(stub: StubOkb, report_enabled: bool) -> dict:
    graph = build_graph(stub)
    return graph.invoke(init_state("TEST-SIG", report_enabled=report_enabled))


def test_report_blocked_when_unverified():
    """待验证条件未清零：不回写（判定未确证不产出置信）。"""
    stub = StubOkb(pending=True)
    state = _run(stub, report_enabled=True)
    assert state["pending_checks"], "前置：应有待验证项"
    assert state["reported"] is False
    assert stub.reported_edges == []


def test_report_blocked_in_test_mode():
    """测试/演示模式（report_enabled=False）：即使判定已确证也不回写。"""
    stub = StubOkb(pending=False)
    state = _run(stub, report_enabled=False)
    assert state["reported"] is False
    assert stub.reported_edges == []


def test_report_writes_when_verified_and_enabled():
    """已确证 + 回写开启：回写主链每跳（生产路径语义）。"""
    stub = StubOkb(pending=False)
    state = _run(stub, report_enabled=True)
    assert state["reported"] is True
    assert stub.reported_edges == ["edge-1"]
