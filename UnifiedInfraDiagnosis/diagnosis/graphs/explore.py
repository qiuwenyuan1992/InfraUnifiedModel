"""探索式诊断：Agent 用工具查数据，过程逐事件可视化。

用户需求（2026-11-24）：把"大模型怎么查数据"在前端拓扑上动态呈现，
含剪枝后的图。核心设计：

- **工具层=现有抽象**：lookup/trace/evidence 走 OkbClient（REST 四原语），
  verify_fact/query_topology 走 InventoryPort——未来对接真实数据源
  （cloudbee 告警流 / UnifiedInfraTopology）换实现零改接口。
- **验证循环闭环**：trace 遇 pending → verify_fact 从拓扑取证（两张图
  合起来的落点）→ 带 facts 重 trace → 条件不满足的边不可走（剪枝）。
- **LLM agent 可选**：配置网关走 OpenAI tools 协议回路（模型自主决定
  查什么）；未配置/失败降级为规则 agent（固定序，相同事件流）——
  AGENTS 降级纪律。
- 事件流（SSE）：step（每次工具调用：tool/args/result/thought/topo 动作）
  → done（conclusion/pruned/facts）→ error。
"""

from __future__ import annotations

import json
from typing import Any, Callable

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

    psu_redundancy：查实体（服务器）的双路供电——冗余侧 PSU 及其上游
    PDU 仍然在图（= 在网）即冗余未失。证据实体返回供前端点亮。
    """
    if fact_key == "psu_redundancy" and entity_id:
        # 冗余侧 PSU（命名约定 entity:psu*）及其上游 PDU 仍在网 = 冗余未失
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


# ---------------------------------------------------------------- 规则 agent（降级路径）
def _rule_agent(signature: str, entity_id: str, tools: dict, emit: Callable) -> dict:
    """固定序降级：lookup → trace → verify（每个 pending）→ re-trace → conclude。"""
    idx = 0

    def step(tool: str, args: dict, result: dict, thought: str, topo: dict) -> None:
        nonlocal idx
        idx += 1
        emit("step", {"index": idx, "tool": tool, "args": args, "result": result,
                      "thought": thought, "topo": topo})

    step("lookup_alert", {"signature": signature}, tools["lookup_alert"](signature=signature),
         "规则路径：归一告警", {"examined": [entity_id] if entity_id else []})
    lookup = tools["lookup_alert"](signature=signature)
    cands = lookup.get("candidates", [])
    if not cands:
        emit("error", {"message": f"告警 {signature} 未登记（lookup 无候选）——走人工登记路径"})
        return {"conclusion": {"summary": f"未登记告警：{signature}"}, "pruned": [], "facts": {}}
    code = cands[0]["stable_code"]

    tr1 = tools["trace_causal"](code=code)
    hops1 = [h for chain in tr1["chains"] for h in chain]
    step("trace_causal", {"code": code}, tr1, "因果推演（未带事实，可能性链）",
         {"examined": [entity_id] if entity_id else []})

    facts: dict[str, str] = {}
    for fk in tr1["pending_checks"]:
        vf = tools["verify_fact"](fact_key=fk, entity_id=entity_id)
        facts[fk] = vf["value"]
        step("verify_fact", {"fact_key": fk, "entity_id": entity_id}, vf,
             f"取证 {fk}（拓扑实例解析）", {"evidence": vf.get("evidence_entities", [])})

    if facts:
        tr2 = tools["trace_causal"](code=code, facts=facts)
        hops2 = {h for chain in tr2["chains"] for h in chain}
        pruned = [h for h in hops1 if h not in hops2]
        step("trace_causal", {"code": code, "facts": facts},
             {**tr2, "pruned": pruned}, "带事实重推演——条件不满足的边被剪枝",
             {"pruned_hint": pruned})
    else:
        tr2, pruned = tr1, []

    if pruned:
        summary = (f"{code}：取证 {facts} 后，{('、'.join(pruned))} 分支被剪枝——"
                   f"结论收窄为无进一步级联（降级运行/无传播）")
    elif hops1:
        summary = f"{code}：沿因果链可发展到 {hops1[-1]}（待验证项 {tr1['pending_checks'] or '无'}）"
    else:
        summary = f"{code}：图上无因果出边（叶子/知识空白）"
    step("submit_conclusion", {"summary": summary}, {"accepted": True},
         "提交结论", {"examined": [entity_id] if entity_id else []})
    return {"conclusion": {"summary": summary}, "pruned": pruned, "facts": facts}


# ---------------------------------------------------------------- LLM agent（tools 协议回路）
def _llm_agent(signature: str, entity_id: str, tools: dict, llm, emit: Callable,
               max_iterations: int = 8) -> dict:
    """模型自主决定查什么：OpenAI tools 协议回路，事件流与规则路径同构。"""
    messages = [{"role": "system", "content": EXPLORE_SYSTEM},
                {"role": "user", "content": f"诊断告警 {signature}，实体 {entity_id or '未指定'}。"}]
    idx = 0
    for _ in range(max_iterations):
        msg = llm.chat_messages(messages, tools=TOOL_SPECS)
        tool_calls = msg.get("tool_calls") or []
        if not tool_calls:
            return {"conclusion": {"summary": msg.get("content", "")}, "pruned": [],
                    "facts": {}, "llm": True}
        messages.append({"role": "assistant", "content": msg.get("content", ""),
                          "tool_calls": tool_calls})
        for tc in tool_calls:
            name = tc["function"]["name"]
            try:
                args = json.loads(tc["function"].get("arguments") or "{}")
            except ValueError:
                args = {}
            fn = tools.get(name)
            result = fn(**args) if fn else {"error": f"未知工具 {name}"}
            idx += 1
            topo: dict = {}
            if name == "lookup_alert" and entity_id:
                topo = {"examined": [entity_id]}
            elif name == "verify_fact":
                topo = {"evidence": result.get("evidence_entities", [])}
            emit("step", {"index": idx, "tool": name, "args": args, "result": result,
                          "thought": msg.get("reasoning_content", ""), "topo": topo})
            messages.append({"role": "tool", "tool_call_id": tc.get("id", ""),
                             "content": json.dumps(result, ensure_ascii=False)[:2000]})
    return {"conclusion": {"summary": "达到最大工具调用轮数，强制收束"}, "pruned": [], "facts": {}}


def run_explore(signature: str, entity_id: str, okb: OkbClient, llm, emit: Callable) -> dict:
    """探索式诊断入口：LLM agent 优先，异常降级规则 agent（同事件流）。"""
    inv = FixtureInventory()
    tools = _build_tools(okb, inv)
    if llm is not None and llm.available():
        try:
            return _llm_agent(signature, entity_id, tools, llm, emit)
        except Exception as exc:  # noqa: BLE001 - LLM 失败降级规则路径
            emit("step", {"index": 0, "tool": "degraded", "args": {},
                          "result": {"reason": str(exc)},
                          "thought": "LLM agent 失败，降级规则路径", "topo": {}})
    return _rule_agent(signature, entity_id, tools, emit)
