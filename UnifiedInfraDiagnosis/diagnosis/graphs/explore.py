"""探索式诊断：LangGraph 图驱动的 Agent 工具调用回路（过程可视化）。

收编（2026-11-24 合图重构一并完成）：此前 LLM 回路是裸 while 循环——
违背"编排归 LangGraph"的模块纪律。现为 decide ⇄ execute 图回路：

    decide（决策：LLM 模式调网关 tools 协议 / 规则模式状态机）
      ├─ 有工具调用 → execute（执行工具、emit step 事件、更新状态）→ decide
      └─ 终答/完成 → END

工具层背靠既有抽象（OkbClient 四原语 + InventoryPort），未来对接真实
数据源换实现零改接口。验证循环：trace 遇 pending → verify_fact 从拓扑
取证 → facts 重 trace → 条件不满足的边不可走（剪枝）。
"""

from __future__ import annotations

import json
from typing import Any, Callable, TypedDict

from langgraph.graph import END, StateGraph

from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.okb import OkbClient

# ---------------------------------------------------------------- 工具规格（OpenAI tools 协议）
TOOL_SPECS = [
    {"type": "function", "function": {
        "name": "lookup_alert",
        "description": "告警归一：signature 查知识库，返回候选异常（stable_code/等级/处置窗口）",
        "parameters": {"type": "object", "properties": {
            "signature": {"type": "string", "description": "告警 signature"}},
            "required": ["signature"]}}},
    {"type": "function", "function": {
        "name": "trace_causal",
        "description": "因果推演：从异常 stable_code 沿因果链推演。可带 facts（已取证事实）——"
                       "条件不满足的边不可走（剪枝）。返回 chains 与 pending_checks",
        "parameters": {"type": "object", "properties": {
            "code": {"type": "string", "description": "起点异常 stable_code"},
            "facts": {"type": "object", "description": "已取证事实 {fact_key: value}"}},
            "required": ["code"]}}},
    {"type": "function", "function": {
        "name": "verify_fact",
        "description": "事实取证：从拓扑实例解析待验证条件（如 psu_redundancy——查双路供电），"
                       "返回取值与证据实体（拓扑上点亮）",
        "parameters": {"type": "object", "properties": {
            "fact_key": {"type": "string"}, "entity_id": {"type": "string"}},
            "required": ["fact_key"]}}},
    {"type": "function", "function": {
        "name": "query_topology",
        "description": "拓扑查询：实体间连接关系（供电下游/链路对端/容器）",
        "parameters": {"type": "object", "properties": {
            "entity_id": {"type": "string"},
            "relation": {"type": "string", "enum": ["power_children", "link_peers", "containers"]}},
            "required": ["entity_id", "relation"]}}},
    {"type": "function", "function": {
        "name": "get_evidence",
        "description": "证据回链：按 edge_id/stable_code 查知识出处片段",
        "parameters": {"type": "object", "properties": {
            "ref": {"type": "string"}}, "required": ["ref"]}}},
    {"type": "function", "function": {
        "name": "submit_conclusion",
        "description": "提交诊断结论（一句话总结）",
        "parameters": {"type": "object", "properties": {
            "summary": {"type": "string"}}, "required": ["summary"]}}},
]

EXPLORE_SYSTEM = """你是基础设施诊断 Agent。你只能通过提供的工具查询数据与知识（告警归一、
因果推演、拓扑取证、证据回链），禁止凭记忆编造任何设备/告警/因果事实。

工作方法：
1. 先 lookup_alert 归一告警
2. trace_causal 推演因果（未取证时条件为 unknown，链为可能性）
3. 遇到 pending_checks 不要猜：用 verify_fact 从拓扑取证，把事实带进 facts 重跑 trace_causal——
   条件不满足的边会被剪枝，结论随之收窄
4. 需要证据出处时用 get_evidence
5. 查证充分后 submit_conclusion 提交一句话结论
回答用中文，简洁。
"""


# ---------------------------------------------------------------- 事实解析（拓扑取证）
def _resolve_fact(fact_key: str, entity_id: str, inv: FixtureInventory) -> dict:
    """从拓扑实例解析待验证条件——两张图合起来的落点。

    psu_redundancy：查实体（服务器）的冗余侧 PSU（命名约定 entity:psu2）
    及其上游 PDU 仍在网（= 在图）即冗余未失。证据实体返回供前端点亮。
    """
    if fact_key == "psu_redundancy" and entity_id:
        snapshot = inv.snapshot()
        psu2 = f"{entity_id}:psu2"
        psu2_exists = psu2 in snapshot["entities"]
        parents = [src for src, dst in snapshot["power_edges"] if dst == psu2]
        redundant = psu2_exists and bool(parents)
        return {
            "fact_key": fact_key, "value": "ok" if redundant else "lost",
            "evidence_entities": [psu2] + parents[:2] if redundant else [entity_id],
        }
    # 未实现的 fact：如实标注无法取证（不猜）
    return {"fact_key": fact_key, "value": "unknown",
            "evidence_entities": [], "note": "拓扑无法解析该事实（未实现/未同步）"}


# ---------------------------------------------------------------- 工具实现（背靠抽象）
def _build_tools(okb: OkbClient, inv: FixtureInventory) -> dict[str, Callable]:
    def lookup_alert(signature: str) -> dict:
        r = okb.lookup(signature, include_draft=True)
        cands = r.get("candidates", [])
        return {"candidates": [{"stable_code": c["stable_code"], "name": c.get("name", ""),
                                "severity": c.get("severity_online", "")} for c in cands[:5]]}

    def trace_causal(code: str, facts: dict | None = None) -> dict:
        tr = okb.trace(code, max_hops=6, top_k=10, include_draft=True, facts=facts or {})
        chains = [[h["target_stable_code"] for h in c.get("hops", [])] for c in tr.get("chains", [])]
        pending = sorted({e["fact_key"] for c in tr.get("chains", [])
                          for h in c.get("hops", []) for e in h.get("evaluations", [])
                          if e.get("result") == "unknown"})
        return {"chains": chains, "pending_checks": pending}

    def verify_fact(fact_key: str, entity_id: str = "") -> dict:
        return _resolve_fact(fact_key, entity_id, inv)

    def query_topology(entity_id: str, relation: str) -> dict:
        if relation == "power_children":
            return {"entities": inv.power_scope({entity_id})}
        if relation == "link_peers":
            return {"entities": inv.link_endpoints([entity_id])}
        if relation == "containers":
            return {"entities": inv.containers({entity_id}, "cabinet")}
        return {"entities": [], "error": f"未知关系 {relation}"}

    def get_evidence(ref: str) -> dict:
        return okb.evidence(ref)

    def submit_conclusion(summary: str) -> dict:
        return {"accepted": True}

    return {f.__name__: f for f in (lookup_alert, trace_causal, verify_fact,
                                    query_topology, get_evidence, submit_conclusion)}


# ---------------------------------------------------------------- 图状态
class ExploreState(TypedDict, total=False):
    """探索回路状态：decide 写 calls/finished，execute 执行并累积事实。"""

    signature: str
    entity_id: str
    mode: str                    # "llm" | "rule"
    # LLM 模式
    messages: list[dict]
    last_reasoning: str
    # 回路控制
    calls: list[dict]
    finished: bool
    idx: int
    steps_done: list[str]
    # 累积事实与结论
    facts: dict[str, str]
    code: str
    hops_first: list[str]
    pending_facts: list[str]
    pruned: list[str]
    conclusion: str
    unmapped: bool


def _parse_call(tc: dict) -> tuple[str, dict]:
    name = tc["function"]["name"]
    try:
        args = json.loads(tc["function"].get("arguments") or "{}")
    except ValueError:
        args = {}
    return name, args


def _topo_action(name: str, args: dict, result: dict, entity_id: str) -> dict:
    if name == "lookup_alert" and entity_id:
        return {"examined": [entity_id]}
    if name == "verify_fact":
        return {"evidence": result.get("evidence_entities", [])}
    return {}


# ---------------------------------------------------------------- decide / execute 节点
def build_explore_graph(okb: OkbClient, llm: Any, tools: dict, emit: Callable,
                        mode: str) -> Any:
    """装配探索回路：decide ⇄ execute（条件循环，LLM/规则同构）。"""

    def decide(state: ExploreState) -> dict:
        if state.get("finished"):
            return {}
        if state.get("unmapped"):
            return {"finished": True}  # 未登记：已 emit error，直接收束
        if mode == "llm":
            msg = llm.chat_messages(state["messages"], tools=TOOL_SPECS)
            tool_calls = msg.get("tool_calls") or []
            reasoning = msg.get("reasoning_content", "")
            if not tool_calls:
                return {"finished": True, "conclusion": msg.get("content", ""),
                        "messages": state["messages"] + [
                            {"role": "assistant", "content": msg.get("content", "")}]}
            return {"calls": tool_calls, "last_reasoning": reasoning or "",
                    "messages": state["messages"] + [
                        {"role": "assistant", "content": msg.get("content", ""),
                         "tool_calls": tool_calls}]}
        # ---- 规则模式状态机（降级路径，与拆分版行为一致） ----
        tools_done = list(state.get("steps_done", []))
        sig, ent = state["signature"], state.get("entity_id", "")
        def call(name: str, args: dict) -> dict:
            return {"id": f"r{state.get('idx', 0)}", "type": "function",
                    "function": {"name": name, "arguments": json.dumps(args)}}
        if "lookup_alert" not in tools_done:
            return {"calls": [call("lookup_alert", {"signature": sig})]}
        if "trace_causal" not in tools_done:
            return {"calls": [call("trace_causal", {"code": state["code"]})]}
        # 逐个取证 pending（未验证的）
        for fk in state.get("pending_facts", []):
            if fk not in state.get("facts", {}):
                return {"calls": [call("verify_fact", {"fact_key": fk, "entity_id": ent})]}
        if state.get("facts") and tools_done.count("trace_causal") < 2:
            return {"calls": [call("trace_causal", {"code": state["code"], "facts": state["facts"]})]}
        # 结论（与拆分版同文案）——finished 由 execute 执行 submit 后设置
        hops1 = state.get("hops_first", [])
        pruned = state.get("pruned", [])
        pend = state.get("pending_facts", [])
        if pruned:
            summary = (f"{state['code']}：取证 {state['facts']} 后，{('、'.join(pruned))} 分支被剪枝——"
                       f"结论收窄为无进一步级联（降级运行/无传播）")
        elif hops1:
            summary = f"{state['code']}：沿因果链可发展到 {hops1[-1]}（待验证项 {pend or '无'}）"
        else:
            summary = f"{state['code']}：图上无因果出边（叶子/知识空白）"
        return {"calls": [call("submit_conclusion", {"summary": summary})]}

    def execute(state: ExploreState) -> dict:
        if state.get("finished"):
            return {}
        out: dict[str, Any] = {"calls": []}
        idx = state.get("idx", 0)
        steps_done = list(state.get("steps_done", []))
        entity_id = state.get("entity_id", "")
        for tc in state.get("calls", []):
            name, args = _parse_call(tc)
            fn = tools.get(name)
            result = fn(**args) if fn else {"error": f"未知工具 {name}"}
            idx += 1
            steps_done.append(name)
            emit("step", {"index": idx, "tool": name, "args": args, "result": result,
                          "thought": state.get("last_reasoning", ""),
                          "topo": _topo_action(name, args, result, entity_id)})
            # 状态积累（两模式共用）
            if name == "lookup_alert":
                cands = result.get("candidates", [])
                if not cands:
                    emit("error", {"message": f"告警 {state['signature']} 未登记（lookup 无候选）——走人工登记路径"})
                    out.update({"unmapped": True, "finished": True,
                                "conclusion": f"未登记告警：{state['signature']}"})
                else:
                    out["code"] = cands[0]["stable_code"]
            elif name == "trace_causal":
                hops = [h for chain in result.get("chains", []) for h in chain]
                if not state.get("hops_first"):
                    out["hops_first"] = hops
                    out["pending_facts"] = result.get("pending_checks", [])
                else:
                    second = set(hops)
                    out["pruned"] = [h for h in state["hops_first"] if h not in second]
            elif name == "verify_fact":
                facts = dict(state.get("facts", {}))
                facts[result["fact_key"]] = result["value"]
                out["facts"] = facts
            elif name == "submit_conclusion":
                out.update({"finished": True, "conclusion": args.get("summary", "")})
            if mode == "llm":
                msgs = list(out.get("messages", state.get("messages", [])))
                msgs.append({"role": "tool", "tool_call_id": tc.get("id", ""),
                             "content": json.dumps(result, ensure_ascii=False)[:2000]})
                out["messages"] = msgs
        out["idx"] = idx
        out["steps_done"] = steps_done
        return out

    # steps_done 不进 TypedDict 展示（内部协议字段）——用 total=False 动态键
    g = StateGraph(ExploreState)
    g.add_node("decide", decide)
    g.add_node("execute", execute)
    g.set_entry_point("decide")
    g.add_conditional_edges("decide", lambda s: "execute" if s.get("calls") and not s.get("finished") else END)
    g.add_edge("execute", "decide")
    return g.compile()


def run_explore(signature: str, entity_id: str, okb: OkbClient, llm, emit: Callable) -> dict:
    """探索式诊断入口：LLM 模式优先，异常降级规则模式（同事件流）。"""
    tools = _build_tools(okb, FixtureInventory())
    mode = "llm" if (llm is not None and llm.available()) else "rule"

    def _init() -> ExploreState:
        state: ExploreState = {"signature": signature, "entity_id": entity_id, "mode": mode,
                               "calls": [], "finished": False, "idx": 0,
                               "facts": {}, "code": "", "hops_first": [],
                               "pending_facts": [], "pruned": [], "conclusion": "",
                               "steps_done": []}
        if mode == "llm":
            state["messages"] = [
                {"role": "system", "content": EXPLORE_SYSTEM},
                {"role": "user", "content": f"诊断告警 {signature}，实体 {entity_id or '未指定'}。"}]
        return state

    def _run(m: str) -> dict:
        graph = build_explore_graph(okb, llm, tools, emit, m)
        return graph.invoke(_init(), config={"recursion_limit": 40})

    if mode == "llm":
        try:
            final = _run("llm")
            return {"conclusion": {"summary": final.get("conclusion", "")},
                    "pruned": final.get("pruned", []), "facts": final.get("facts", {}),
                    "code": final.get("code", ""), "chain": final.get("hops_first", []),
                    "llm": True}
        except Exception as exc:  # noqa: BLE001 - LLM 失败降级规则路径
            emit("step", {"index": 0, "tool": "degraded", "args": {},
                          "result": {"reason": str(exc)},
                          "thought": "LLM agent 失败，降级规则路径", "topo": {}})
    final = _run("rule")
    return {"conclusion": {"summary": final.get("conclusion", "")},
            "pruned": final.get("pruned", []), "facts": final.get("facts", {}),
            "code": final.get("code", ""), "chain": final.get("hops_first", [])}
