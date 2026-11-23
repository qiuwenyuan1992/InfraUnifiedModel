"""机柜掉电判定序（金路径场景）：告警 → lookup → 跨域取证提示 → 诊断结论 → report 回写。

判定序依据《跨域关联全景》掉电场景 A：
1. 归一入口：告警 signature → OKB lookup → 候选异常（含 severity/clock_hint）
2. 传播推演：对候选起点 trace（facts 可选注入观测事实）
3. 跨域取证提示：same_event 对端（同柜 PDU 掉电）作为待检查项输出
4. 证据回链：每条判定边附 edge_id + revision_id
5. 飞轮回写：report confirmed/rejected（判定置信的边）

确定性节点为主，无 LLM 节点——本图是判定序的最小金路径骨架；
LLM 介入点（告警语义化/结论总结）后续按需插入，必须可降级为规则节点。
"""

from __future__ import annotations

import operator
from typing import Annotated, Any, TypedDict

from langgraph.graph import END, StateGraph

from diagnosis.tools.okb import OkbClient


class PowerOutageState(TypedDict):
    """掉电诊断会话状态（F1：会话状态归编排层，checkpointer 持久化）。"""

    alert_signature: str
    candidates: list[dict]          # lookup 候选（含 severity/clock/peers）
    start_code: str                 # 选定的 trace 起点
    trace_result: dict              # OKB trace 输出（chains + relations）
    pending_checks: Annotated[list[dict], operator.add]  # 待验证检查项（facts unknown 的条件）
    cross_domain_peers: list[dict]  # same_event 对端（跨域取证提示）
    conclusion: dict                # 诊断结论（含 evidence 回链）
    reported: bool                  # report 回写完成标记
    error: str                      # 非致命错误记录


def normalize_alert(okb: OkbClient):
    """节点1：告警归一——signature → 候选异常。"""

    def node(state: PowerOutageState) -> dict:
        result = okb.lookup(state["alert_signature"], include_draft=True)
        candidates = result.get("candidates", [])
        if not candidates:
            return {"error": f"lookup 未命中告警 {state['alert_signature']}：未登记告警走人工登记路径"}
        # 掉电场景金路径：取置信度最高候选为 trace 起点
        return {"candidates": candidates, "start_code": candidates[0]["stable_code"]}

    return node


def propagate_trace(okb: OkbClient):
    """节点2：传播推演——沿因果链遍历，收集待验证检查项（unknown 条件）。"""

    def node(state: PowerOutageState) -> dict:
        if state.get("error"):
            return {}
        trace_result = okb.trace(state["start_code"], max_hops=6, top_k=10, include_draft=True)
        pending: list[dict] = []
        for chain in trace_result.get("chains", []):
            for hop in chain.get("hops", []):
                for ev in hop.get("evaluations", []) or []:
                    if ev.get("result") == "unknown":
                        pending.append({
                            "fact_key": ev["fact_key"], "op": ev["op"],
                            "edge_id": hop["edge_id"],
                            "revision_id": hop.get("revision_id", ""),
                        })
        return {"trace_result": trace_result, "pending_checks": pending}

    return node


def cross_domain_hints(okb: OkbClient):
    """节点3：跨域取证提示——same_event 对端即"同柜/同链路另一面"，交取证。"""

    def node(state: PowerOutageState) -> dict:
        if state.get("error"):
            return {}
        relations = state["trace_result"].get("relations", []) or []
        peers = [
            {"kind": r["edge_kind"], "other_code": r["other_stable_code"],
             "other_severity": r.get("other_severity_online", ""), "edge_id": r["edge_id"]}
            for r in relations if r["edge_kind"] == "same_event"
        ]
        return {"cross_domain_peers": peers}

    return node


def conclude(_okb: OkbClient):
    """节点4：结论生成——纯规则节点：主链 + 待验证项 + 跨域提示 + 证据回链。"""

    def node(state: PowerOutageState) -> dict:
        if state.get("error"):
            return {"conclusion": {"status": "unmapped_alert", "error": state["error"]}}
        chains = state["trace_result"].get("chains", [])
        primary = chains[0] if chains else None
        conclusion: dict[str, Any] = {
            "status": "candidate_chains",
            "start_code": state["start_code"],
            "severity": state["candidates"][0].get("severity_online", ""),
            "clock_hint": state["candidates"][0].get("clock_hint", ""),
            "primary_chain": [
                {"target": h["target_stable_code"], "evidence_level": h["evidence_level"],
                 "revision_id": h.get("revision_id", "")}
                for h in (primary or {}).get("hops", [])
            ],
            "pending_checks": state.get("pending_checks", []),
            "cross_domain_peers": state.get("cross_domain_peers", []),
        }
        return {"conclusion": conclusion}

    return node


def report_feedback(okb: OkbClient):
    """节点5：飞轮回写——判定使用的边 report confirmed（幂等，diagnosis_ref 唯一）。"""

    def node(state: PowerOutageState) -> dict:
        if state.get("error"):
            return {"reported": False}
        chains = state["trace_result"].get("chains", [])
        if not chains:
            return {"reported": False}
        ref = f"diagnosis:{state['alert_signature']}"
        for hop in chains[0].get("hops", []):
            try:
                okb.report(ref, hop["edge_id"], "confirmed", notes="金路径自动回写")
            except Exception:  # noqa: BLE001 - 飞轮回写失败不阻塞诊断输出
                return {"reported": False}
        return {"reported": True}

    return node


def build_graph(okb: OkbClient) -> StateGraph:
    """装配掉电判定序图。"""
    g = StateGraph(PowerOutageState)
    g.add_node("normalize_alert", normalize_alert(okb))
    g.add_node("propagate_trace", propagate_trace(okb))
    g.add_node("cross_domain_hints", cross_domain_hints(okb))
    g.add_node("conclude", conclude(okb))
    g.add_node("report_feedback", report_feedback(okb))
    g.add_edge("__start__", "normalize_alert")
    g.add_edge("normalize_alert", "propagate_trace")
    g.add_edge("propagate_trace", "cross_domain_hints")
    g.add_edge("cross_domain_hints", "conclude")
    g.add_edge("conclude", "report_feedback")
    g.add_edge("report_feedback", END)
    return g.compile()


def diagnose(alert_signature: str, okb: OkbClient | None = None) -> dict:
    """跑一次掉电诊断；返回终态。"""
    okb = okb or OkbClient()
    graph = build_graph(okb)
    init: PowerOutageState = {
        "alert_signature": alert_signature,
        "candidates": [], "start_code": "", "trace_result": {},
        "pending_checks": [], "cross_domain_peers": [],
        "conclusion": {}, "reported": False, "error": "",
    }
    return graph.invoke(init)
