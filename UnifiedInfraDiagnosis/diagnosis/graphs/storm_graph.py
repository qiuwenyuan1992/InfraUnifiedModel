"""告警风暴根因反推判定序（LangGraph 图）：多告警 → 公共供电祖先 = 疑似根因。

收编（2026-11-23 复核偏差③）：此前 infer_power_root 是游离 REST 逻辑；
现为四节点判定序，与正向诊断（judge_graph）同机制——可流式、节点可插
LLM（语义化/结论总结座位留白）。算法本体（纯函数）仍住 storm.py：
图节点调用纯函数，规则可单测。

节点序：ingest_alerts → infer_common_ancestor → verify_against_okb → conclude
"""

from __future__ import annotations

from typing import Any, TypedDict

from langgraph.graph import END, StateGraph

from diagnosis.graphs.storm import infer_power_root
from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.okb import OkbClient


class StormState(TypedDict):
    """风暴诊断会话状态。"""

    alerts: list[dict]             # 输入告警（entity, stable_code）
    grouped: dict                  # ingest 产物：同型告警分组
    root_cause: dict | None        # 疑似根因（None=独立故障，如实不硬凑）
    affected_entities: list[str]
    independent: bool
    verify: str                    # knowledge_backed / topology_inferred / unverified
    conclusion: dict               # 结论（summary 人话 + rationale）
    error: str


def ingest_alerts(_okb: OkbClient):
    """节点1：告警接入与同型分组（多类型风暴时按 stable_code 分组，取最大组反推）。"""

    def node(state: StormState) -> dict:
        alerts = state["alerts"]
        if not alerts:
            return {"error": "空告警列表"}
        groups: dict[str, list[dict]] = {}
        for a in alerts:
            groups.setdefault(a["stable_code"], []).append(a)
        main = max(groups.values(), key=len)
        return {"grouped": {"code": main[0]["stable_code"], "alerts": main,
                            "groups": {k: len(v) for k, v in groups.items()}}}

    return node


def infer_common_ancestor(_okb: OkbClient):
    """节点2：最近公共供电祖先反推（算法住 storm.py 纯函数）。"""

    def node(state: StormState) -> dict:
        if state.get("error"):
            return {}
        res = infer_power_root(state["grouped"]["alerts"], FixtureInventory())
        return {
            "root_cause": res.get("root_cause"),
            "affected_entities": res.get("affected_entities", []),
            "independent": res["independent"],
            "verify": "unverified",
        }

    return node


def verify_against_okb(okb: OkbClient):
    """节点3：与 OKB 因果图交叉验证（trace 可达/same_event 对端）。"""

    def node(state: StormState) -> dict:
        root = state.get("root_cause")
        if state.get("error") or not root:
            return {}
        alert_code = state["grouped"]["code"]
        verify = "topology_inferred"
        try:
            tr = okb.trace(root["stable_code"], max_hops=6, include_draft=True)
            targets = {h["target_stable_code"]
                       for c in tr.get("chains", []) for h in c.get("hops", [])}
            if alert_code in targets:
                verify = "knowledge_backed"
            else:
                tr2 = okb.trace(alert_code, max_hops=3, include_draft=True)
                peers = {r["other_stable_code"] for r in tr2.get("relations", [])
                         if r.get("edge_kind") == "same_event"}
                if root["stable_code"] in peers:
                    verify = "knowledge_backed"
        except Exception:  # noqa: BLE001 - OKB 不可达如实降级
            pass
        # 同步进 root_cause（API 兼容字段）
        return {"verify": verify, "root_cause": {**root, "verify": verify}}

    return node


def conclude(_okb: OkbClient):
    """节点4：结论生成——人话 summary + rationale + 证据标注。"""

    def node(state: StormState) -> dict:
        if state.get("error"):
            return {"conclusion": {"summary": state["error"], "rationale": state["error"]}}
        if state["independent"] or not state["root_cause"]:
            return {"conclusion": {
                "summary": f"{len(state['affected_entities'])} 条告警无共同供电点：独立故障，不硬凑根因——各告警走正向诊断",
                "rationale": "无共同供电点（跨供电域或单点告警）",
            }}
        root = state["root_cause"]
        n = len(state["affected_entities"])
        if state["verify"] == "knowledge_backed":
            verify_note = "OKB 因果图验证通过"
        else:
            verify_note = "拓扑推断（OKB 未覆盖该因果路径，知识空白如实标注）"
        return {"conclusion": {
            "summary": (f"疑似上层故障：{root['entity']}（{root['stable_code']}）——"
                        f"{n} 条 PSU 告警的最近公共供电祖先；{verify_note}"),
            "rationale": (f"{n} 条同型告警的共同供电点是 {root['entity']}；{verify_note}"),
        }}

    return node


def build_storm_graph(okb: OkbClient) -> Any:
    """装配风暴判定序图。"""
    g = StateGraph(StormState)
    g.add_node("ingest_alerts", ingest_alerts(okb))
    g.add_node("infer_common_ancestor", infer_common_ancestor(okb))
    g.add_node("verify_against_okb", verify_against_okb(okb))
    g.add_node("conclude", conclude(okb))
    g.add_edge("__start__", "ingest_alerts")
    g.add_edge("ingest_alerts", "infer_common_ancestor")
    g.add_edge("infer_common_ancestor", "verify_against_okb")
    g.add_edge("verify_against_okb", "conclude")
    g.add_edge("conclude", END)
    return g.compile()


def init_storm_state(alerts: list[dict]) -> StormState:
    return {
        "alerts": alerts, "grouped": {}, "root_cause": None,
        "affected_entities": [], "independent": False, "verify": "unverified",
        "conclusion": {}, "error": "",
    }
