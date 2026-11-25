"""合图（unified）：一张诊断图，条件路由两种输入形态。

重构（2026-11-24 用户拍板"全部做"）：judge_graph（正向）与 storm_graph
（反推）合并——单条 vs 多条告警是输入形态差异，不是场景差异（原则：
场景差异住 OKB 不住流程）。

    （条件入口：按告警条数路由）
      ├─ 1 条 → normalize_alert → propagate_trace → cross_domain_hints
      │          → impact_radius → conclude → report_feedback → END
      └─ N 条 → ingest_alerts → infer_common_ancestor → verify_against_okb
                                    → conclude → report_feedback → END

conclude 为共享调度节点（按状态形态分流：有 alerts 走风暴结论逻辑，
否则正向结论逻辑）；report_feedback 两分支共享（风暴无链时不回写，
守卫在节点内）。节点实现复用 judge_graph/storm_graph 的工厂——行为不变。
"""

from __future__ import annotations

import operator
from typing import Annotated, Any, TypedDict

from langgraph.graph import END, StateGraph

from diagnosis.tools.okb import OkbClient


class UnifiedState(TypedDict, total=False):
    """合图状态：正向与风暴字段的并集（各分支只写自己的字段）。"""

    # 输入
    alert_signature: str
    entity_id: str
    alerts: list[dict]          # 风暴输入（N 条）——同时是分支调度标记
    report_enabled: bool
    # 正向分支产物
    candidates: list[dict]
    start_code: str
    trace_result: dict
    pending_checks: Annotated[list[dict], operator.add]  # reducer 通道（与 judge 图一致）
    cross_domain_peers: list[dict]
    impact_radius: dict
    conclusion: dict
    reported: bool
    error: str
    # 风暴分支产物
    grouped: dict
    root_cause: dict | None
    affected_entities: list[str]
    independent: bool
    verify: str


def build_unified_graph(okb: OkbClient) -> Any:
    """装配合图：复用既有节点工厂（节点实现不动，仅装配变化）。"""
    from diagnosis.graphs.judge_graph import (
        conclude as judge_conclude,
        cross_domain_hints,
        impact_radius_node,
        normalize_alert,
        propagate_trace,
        report_feedback,
    )
    from diagnosis.graphs.storm_graph import (
        conclude as storm_conclude,
        infer_common_ancestor,
        ingest_alerts,
        verify_against_okb,
    )

    judge_conclude_node = judge_conclude(okb)
    storm_conclude_node = storm_conclude(okb)

    def conclude_dispatch(state: UnifiedState) -> dict:
        """共享结论节点：按状态形态分流（行为与拆分版各自一致）。"""
        if "alerts" in state:
            return storm_conclude_node(state)
        return judge_conclude_node(state)

    def route_by_alert_count(state: UnifiedState) -> str:
        return "storm_branch" if len(state.get("alerts") or []) >= 2 else "judge_branch"

    g = StateGraph(UnifiedState)

    # ---- 分支装配 ----
    g.add_node("normalize_alert", normalize_alert(okb))
    g.add_node("propagate_trace", propagate_trace(okb))
    g.add_node("cross_domain_hints", cross_domain_hints(okb))
    g.add_node("impact_radius", impact_radius_node(okb))
    g.add_node("ingest_alerts", ingest_alerts(okb))
    g.add_node("infer_common_ancestor", infer_common_ancestor(okb))
    g.add_node("verify_against_okb", verify_against_okb(okb))
    g.add_node("conclude", conclude_dispatch)
    g.add_node("report_feedback", report_feedback(okb))

    g.set_conditional_entry_point(route_by_alert_count, {
        "judge_branch": "normalize_alert",
        "storm_branch": "ingest_alerts",
    })

    # 正向链
    g.add_edge("normalize_alert", "propagate_trace")
    g.add_edge("propagate_trace", "cross_domain_hints")
    g.add_edge("cross_domain_hints", "impact_radius")
    g.add_edge("impact_radius", "conclude")

    # 风暴链
    g.add_edge("ingest_alerts", "infer_common_ancestor")
    g.add_edge("infer_common_ancestor", "verify_against_okb")
    g.add_edge("verify_against_okb", "conclude")

    # 共享出口：两分支结论都过飞轮回写节点（守卫在节点内各自判断）
    g.add_edge("conclude", "report_feedback")
    g.add_edge("report_feedback", END)

    return g.compile()
