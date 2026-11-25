"""通用故障诊断判定序（正向）：告警 → 归一 → 传播推演 → 跨域提示 → 影响半径 → 结论 → 飞轮回写。

原 rack_power.py（掉电场景金路径骨架）演进为通用判定序：11 样本跨
服务器/电力/网络/制冷五域走同一图——场景差异住 OKB（因果知识）与
InventoryPort（拓扑展开），流程本身无场景分支。判定序骨架：

1. 归一入口：告警 signature → OKB lookup → 候选异常（含 severity/clock_hint）
2. 传播推演：对候选起点 trace（facts 可选注入观测事实）
3. 跨域取证提示：same_event 对端作为待检查项输出
4. 影响半径：trace 结果 × InventoryPort 拓扑展开（需 entity_id）
5. 结论生成：主链 + 证据回链（edge_id + revision_id）+ 一句话总结
6. 飞轮回写：report confirmed（守卫：pending 未清零/测试模式不回写）

确定性节点为主，无 LLM 节点——LLM 介入点（告警语义化/结论总结）
后续按需插入，必须可降级为规则节点。骨架最初依据《跨域关联全景》
掉电场景 A，金路径回归见 tests/test_judge_golden.py。
"""

from __future__ import annotations

import operator
from typing import Annotated, Any, TypedDict

from langgraph.graph import END, StateGraph

from diagnosis.graphs.impact import compute_impact_radius
from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.okb import OkbClient


class PowerOutageState(TypedDict):
    """掉电诊断会话状态（F1：会话状态归编排层，checkpointer 持久化）。"""

    alert_signature: str
    entity_id: str                  # 告警实体（可选：无实体输入时影响半径跳过）
    report_enabled: bool            # 飞轮回写开关（测试/演示关闭——防置信度污染）
    candidates: list[dict]          # lookup 候选（含 severity/clock/peers）
    start_code: str                 # 选定的 trace 起点
    trace_result: dict              # OKB trace 输出（chains + relations）
    pending_checks: Annotated[list[dict], operator.add]  # 待验证检查项（facts unknown 的条件）
    cross_domain_peers: list[dict]  # same_event 对端（跨域取证提示）
    impact_radius: dict             # 影响半径（因果图×拓扑合遍历的预期告警集）
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
        seen: set[tuple[str, str]] = set()  # 多链共享前缀时按 (fact_key, edge_id) 去重
        for chain in trace_result.get("chains", []):
            for hop in chain.get("hops", []):
                for ev in hop.get("evaluations", []) or []:
                    if ev.get("result") == "unknown":
                        key = (ev["fact_key"], hop["edge_id"])
                        if key in seen:
                            continue
                        seen.add(key)
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
        peers = []
        seen_peers: set[tuple[str, str]] = set()
        for r in relations:
            if r["edge_kind"] != "same_event":
                continue
            key = (r["edge_id"], r["other_stable_code"])
            if key in seen_peers:
                continue
            seen_peers.add(key)
            peers.append({
                "kind": r["edge_kind"], "other_code": r["other_stable_code"],
                "other_severity": r.get("other_severity_online", ""), "edge_id": r["edge_id"],
            })
        return {"cross_domain_peers": peers}

    return node


def impact_radius_node(_okb: OkbClient, inventory: FixtureInventory | None = None):
    """节点3.5：影响半径——trace 结果 × 拓扑实例展开（两张图合遍历）。

    无实体输入（纯 signature）或 lookup 未命中时跳过；绑定规则为确定性
    规则表（LLM 座位降级实现：将来 LLM 读边语义选展开关系，展开仍由引擎
    执行，证据链可回归）。制冷域拓扑未同步 → 绑定不可用，如实标记。
    """

    def node(state: PowerOutageState) -> dict:
        if state.get("error") or not state.get("entity_id"):
            return {}
        inv = inventory or FixtureInventory()
        radius = compute_impact_radius(state["trace_result"], state["entity_id"], inv)
        return {"impact_radius": radius}

    return node


def conclude(_okb: OkbClient):
    """节点4：结论生成——主链 + 待验证项 + 跨域提示 + 证据回链 + 一句话人话总结。

    summary 为规则降级实现（LLM 结论生成座位的兜底）：把碎片证据拼成人话。
    """

    def node(state: PowerOutageState) -> dict:
        if state.get("error"):
            return {"conclusion": {
                "status": "unmapped_alert", "error": state["error"],
                "summary": f"未登记告警（{state['alert_signature']}），知识库无映射——请走人工登记，不猜测。",
            }}
        chains = state["trace_result"].get("chains", [])
        primary = chains[0] if chains else None
        cand = state["candidates"][0]
        hops = (primary or {}).get("hops", [])
        conclusion: dict[str, Any] = {
            "status": "candidate_chains",
            "start_code": state["start_code"],
            "severity": cand.get("severity_online", ""),
            "clock_hint": cand.get("clock_hint", ""),
            "primary_chain": [
                {"target": h["target_stable_code"], "evidence_level": h["evidence_level"],
                 "revision_id": h.get("revision_id", "")}
                for h in hops
            ],
            "pending_checks": state.get("pending_checks", []),
            "cross_domain_peers": state.get("cross_domain_peers", []),
        }

        # ---- 一句话总结（规则版）：类型层结论 + 影响半径 + 取证建议
        name = cand.get("name", state["start_code"])
        radius = state.get("impact_radius") or {}
        n_expected = len(radius.get("expected_alerts", []))
        parts: list[str] = []
        if not hops:
            if state["trace_result"].get("relations"):
                parts.append(f"{name}：图上无因果出边（末端观测/知识空白），无法推演后续影响")
            else:
                parts.append(f"{name}：知识空白（无因果出边、无关联关系）")
        else:
            parts.append(f"{name}：沿因果链可发展到 {hops[-1]['target_stable_code']}")
        pend = state.get("pending_checks", [])
        if pend:
            keys = "、".join(sorted({p["fact_key"] for p in pend}))
            parts.append(f"需先取证验证：{keys}")
        peers = state.get("cross_domain_peers", [])
        if peers:
            parts.append(f"建议同步核查对端（{peers[0]['other_code']} 等 {len(peers)} 项）")
        if n_expected:
            n_ent = len({a["entity"] for a in radius["expected_alerts"]})
            parts.append(f"影响半径：预计波及 {n_ent} 个实体的 {n_expected} 条告警")
        conclusion["summary"] = "；".join(parts) + "。"
        return {"conclusion": conclusion}

    return node


def report_feedback(okb: OkbClient):
    """节点5：飞轮回写——判定使用的边 report confirmed（幂等，diagnosis_ref 唯一）。

    污染防护：pending_checks 未清零时不回写（判定未验证，不产出置信）；
    测试/演示路径（report_enabled=False）不回写——测试反复跑不抬升
    知识置信度（此前宕机边被测试跑成 curated 即此教训）。
    """

    def node(state: PowerOutageState) -> dict:
        if state.get("error") or not state.get("report_enabled", True):
            return {"reported": False}
        # 合图共享节点：风暴分支无 trace_result（如实不回写）
        chains = (state.get("trace_result") or {}).get("chains", [])
        if not chains:
            return {"reported": False}
        # 判定未验证（存在 unknown 待取证项）不回写：只回写已确证的判定
        if state.get("pending_checks"):
            return {"reported": False}
        ref = f"diagnosis:{state['alert_signature']}"
        for hop in chains[0].get("hops", []):
            try:
                okb.report(ref, hop["edge_id"], "confirmed", notes="判定序自动回写")
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
    g.add_node("impact_radius", impact_radius_node(okb))
    g.add_node("conclude", conclude(okb))
    g.add_node("report_feedback", report_feedback(okb))
    g.add_edge("__start__", "normalize_alert")
    g.add_edge("normalize_alert", "propagate_trace")
    g.add_edge("propagate_trace", "cross_domain_hints")
    g.add_edge("cross_domain_hints", "impact_radius")
    g.add_edge("impact_radius", "conclude")
    g.add_edge("conclude", "report_feedback")
    g.add_edge("report_feedback", END)
    return g.compile()


def init_state(alert_signature: str, entity_id: str | None = None,
               report_enabled: bool = True) -> PowerOutageState:
    """构造诊断初始态（API/CLI/测试共用）；entity 可选，给定时计算影响半径。

    report_enabled=False 供测试/演示：跑完整判定序但不回写知识置信度。
    """
    return {
        "alert_signature": alert_signature,
        "entity_id": entity_id or "",
        "report_enabled": report_enabled,
        "candidates": [], "start_code": "", "trace_result": {},
        "pending_checks": [], "cross_domain_peers": [],
        "impact_radius": {},
        "conclusion": {}, "reported": False, "error": "",
    }


def diagnose(alert_signature: str, okb: OkbClient | None = None, entity_id: str | None = None,
             report_enabled: bool = True) -> dict:
    """跑一次掉电诊断；返回终态。测试/演示传 report_enabled=False 防污染。"""
    okb = okb or OkbClient()
    graph = build_graph(okb)
    return graph.invoke(init_state(alert_signature, entity_id=entity_id, report_enabled=report_enabled))
