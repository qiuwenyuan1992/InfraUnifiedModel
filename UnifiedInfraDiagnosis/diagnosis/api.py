"""诊断 API：FastAPI + LangGraph 流式接口。

POST /api/diagnosis/run   —— SSE 流式推送判定序执行事件（节点进度/中间产物/终态）
POST /api/diagnosis/report —— 已有 CLI 同款同步入口（非流式，测试用）

设计（用户确认 2026-11-21）：
- 方案 A：诊断接口由本模块（Python/LangGraph 同进程）直接暴露，Web 经 Vite 代理调用
- 流式：LangGraph stream_mode="updates" 逐节点推送执行事件
- 数据展示：结论带 edge_id/revision_id/stable_code，前端可直接跳转 OKB 图谱/证据页
- 可观测（真实告警流/指标）暂未对接：输入侧为模拟告警 signature，接口形态不变，
  将来接告警流时替换输入源即可
"""

from __future__ import annotations

import asyncio
import json
import queue
import threading
from typing import Any, AsyncIterator

from fastapi import FastAPI
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from diagnosis.graphs.unified_graph import build_unified_graph
from diagnosis.graphs.judge_graph import diagnose, init_state
from diagnosis.graphs.storm_graph import init_storm_state
from diagnosis.samples import SAMPLES, normalize
from diagnosis.tools.inventory import FixtureInventory
from diagnosis.tools.llm import LlmClient, SYSTEM_PROMPT, build_diagnosis_context
from diagnosis.tools.okb import OkbClient

app = FastAPI(title="UnifiedInfraDiagnosis API", version="0.1.0")


class DiagnoseRequest(BaseModel):
    alert_signature: str = ""        # 直接给 signature（兼容既有调用/手工输入）
    equipment: str | None = None     # 部位：与 fault_type 同时给定时走归一路径（告警原生形态）
    fault_type: str | None = None    # 故障类型
    timestamp: str | None = None     # 时间点：暂不参与判定（时间窗/风暴对账后续接入）
    entity_id: str | None = None     # 告警实体：给定时计算影响半径（因果图×拓扑合遍历）
    include_draft: bool = True


def _event(name: str, payload: Any) -> str:
    """SSE 帧：event: <name>\\ndata: <json>\\n\\n。"""
    return f"event: {name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"


@app.post("/api/diagnosis/run")
async def run_diagnosis(req: DiagnoseRequest) -> StreamingResponse:
    """流式诊断：逐节点推送判定序执行事件，终态以 conclusion 事件结束。

    本页为测试控制台：report_enabled=False——测试诊断永不回写知识置信
    （飞轮只吃真实告警流的诊断；历史上宕机边被测试跑成 curated 的教训）。

    事件序列：
    - node_start  {node}                     节点开始
    - node_update {node, data}               节点产出（中间产物）
    - done        {conclusion, reported}     终态（含证据回链字段）
    - error       {message}                  未命中/异常（终态）
    """
    okb = OkbClient()
    graph = build_unified_graph(okb)   # 合图：ingest 条件路由（1 条走正向分支）
    signature = req.alert_signature
    if req.equipment and req.fault_type:
        # 告警原生形态（部位+类型）→ 归一；未登记组合走人工路径（不编造）
        signature = normalize(req.equipment, req.fault_type) or f"UNREGISTERED:{req.equipment}.{req.fault_type}"
    state = init_state(signature, entity_id=req.entity_id, report_enabled=False)

    async def gen() -> AsyncIterator[str]:
        # 单次执行聚合终态：updates 流逐节点推送，同时累积各节点产物——
        # 不做二次 invoke（report 幂等但诊断只执行一遍，重跑浪费且语义不对）。
        aggregated: dict = dict(state)
        try:
            for chunk in graph.stream(state, stream_mode="updates"):
                for node_name, node_out in chunk.items():
                    if not isinstance(node_out, dict):
                        continue
                    yield _event("node_update", {"node": node_name, "data": node_out})
                    for k, v in node_out.items():
                        # reducer 字段（pending_checks 为 operator.add）累积合并，其余覆盖
                        if k == "pending_checks" and isinstance(aggregated.get(k), list):
                            aggregated[k] = aggregated[k] + (v or [])
                        else:
                            aggregated[k] = v
            yield _event(
                "done",
                {
                    "conclusion": aggregated.get("conclusion", {}),
                    "reported": aggregated.get("reported", False),
                    "candidates": aggregated.get("candidates", []),
                    "pending_checks": aggregated.get("pending_checks", []),
                    "cross_domain_peers": aggregated.get("cross_domain_peers", []),
                    "impact_radius": aggregated.get("impact_radius", {}),
                    "error": aggregated.get("error", ""),
                },
            )
        except Exception as exc:  # noqa: BLE001 - SSE 必须以 error 帧收尾
            yield _event("error", {"message": str(exc)})

    async def gen_with_cache() -> AsyncIterator[str]:
        # 缓存终态供 /chat 上下文注入（诊断对话座位）
        async for chunk in gen():
            try:
                data = json.loads(chunk.split("data: ", 1)[1].strip())
            except (ValueError, IndexError):
                data = {}
            if "conclusion" in data:
                _LAST_DIAGNOSIS.clear()
                _LAST_DIAGNOSIS.update({**state, **data})
            yield chunk

    return StreamingResponse(gen_with_cache(), media_type="text/event-stream")


@app.get("/api/diagnosis/healthz")
async def healthz() -> dict:
    okb = OkbClient()
    return {"status": "ok", "okb": okb.healthz()}


@app.get("/api/diagnosis/samples")
async def list_samples() -> dict:
    """测试样本集（samples.py 实时读取）：测试页展示与一键载入。"""
    return {"samples": SAMPLES}


@app.get("/api/diagnosis/topology")
async def fixture_topology() -> dict:
    """fixture 拓扑快照：机柜连接关系（供电树/端口链路/空间包含）。"""
    return FixtureInventory().snapshot()


class StormAlert(BaseModel):
    entity: str          # 告警实体（如 srv-001:psu1）
    stable_code: str     # 异常类型（如 server.psu.input_lost）


class StormRequest(BaseModel):
    alerts: list[StormAlert]


@app.post("/api/diagnosis/storm")
async def storm_diagnosis(req: StormRequest) -> StreamingResponse:
    """告警风暴根因反推：走合图风暴分支（N 条告警条件路由，SSE 流式）。

    与正向 /run 同一张图（unified_graph）：ingest 按告警条数路由到风暴
    分支；node_update 逐节点推送（步骤条联动），done 带终态。
    """
    okb = OkbClient()
    graph = build_unified_graph(okb)
    state = init_storm_state(
        [{"entity": a.entity, "stable_code": a.stable_code} for a in req.alerts])

    async def gen() -> AsyncIterator[str]:
        aggregated: dict = dict(state)
        try:
            for chunk in graph.stream(state, stream_mode="updates"):
                for node_name, node_out in chunk.items():
                    if not isinstance(node_out, dict):
                        continue
                    yield _event("node_update", {"node": node_name, "data": node_out})
                    aggregated.update(node_out)
            root = aggregated.get("root_cause")
            conclusion = aggregated.get("conclusion", {})
            result = {
                "independent": aggregated.get("independent", False),
                "root_cause": {**root, "verify": aggregated.get("verify", "")} if root else None,
                "affected_entities": aggregated.get("affected_entities", []),
                "alert_count": len(aggregated.get("affected_entities", [])),
                "rationale": conclusion.get("rationale", ""),
                "conclusion": conclusion,
            }
            # 写入对话上下文缓存（风暴结果同样可被追问）
            _LAST_DIAGNOSIS.clear()
            _LAST_DIAGNOSIS.update({**aggregated, "kind": "storm"})
            yield _event("done", result)
        except Exception as exc:  # noqa: BLE001 - SSE 必须以 error 帧收尾
            yield _event("error", {"message": str(exc)})

    return StreamingResponse(gen(), media_type="text/event-stream")


@app.get("/api/diagnosis/evidence")
async def evidence(ref: str) -> dict:
    """证据回链：edge_id / stable_code → OKB 出处片段（可审计）。"""
    if not ref.strip():
        from fastapi import HTTPException
        raise HTTPException(status_code=422, detail="ref 不能为空")
    return OkbClient().evidence(ref)


@app.get("/api/diagnosis/graph")
async def langgraph_mermaid(which: str = "judge") -> dict:
    """LangGraph 运行图（mermaid 语法）：which=judge 正向判定序 / storm 风暴反推。"""
    okb = OkbClient()
    # 合图：judge/storm 是同一张图的两个分支（which 选分支节点序返回）
    graph = build_unified_graph(okb)
    try:
        mermaid = graph.get_graph().draw_mermaid()
    except Exception as exc:  # noqa: BLE001 - draw 失败降级为节点清单
        mermaid = ""
    nodes = ["__start__"] + [n for n in graph.nodes] + ["__end__"] if mermaid else list(graph.nodes)
    branches = {
        "judge": ["normalize_alert", "propagate_trace", "cross_domain_hints", "impact_radius", "conclude", "report_feedback"],
        "storm": ["ingest_alerts", "infer_common_ancestor", "verify_against_okb", "conclude", "report_feedback"],
    }
    return {"which": which, "mermaid": mermaid, "nodes": nodes,
            "branch": branches.get(which, [])}


class ExploreRequest(BaseModel):
    alert_signature: str
    entity_id: str = ""


@app.post("/api/diagnosis/explore")
async def explore_diagnosis(req: ExploreRequest) -> StreamingResponse:
    """探索式诊断（SSE）：Agent 用工具查数据，过程逐事件可视化。

    事件流：step（每次工具调用：tool/args/result/thought/topo 动作——前端
    拓扑动态呈现的数据源）→ done（conclusion/pruned/facts）→ error。
    LLM 配置时走模型自主回路（OpenAI tools 协议），否则规则降级
    （固定序，同事件流）——AGENTS 降级纪律。
    """
    from diagnosis.graphs.explore import run_explore

    okb = OkbClient()
    llm = LlmClient()

    # 真流式：worker 线程 emit 即入队，gen 逐帧转发（LLM 多轮期间持续推送）
    q: "queue.Queue[tuple[str, dict] | None]" = queue.Queue()

    def worker() -> None:
        try:
            result = run_explore(req.alert_signature, req.entity_id, okb, llm,
                                 emit=lambda ev, p: q.put((ev, p)))
        except Exception as exc:  # noqa: BLE001 - 异常转 error 帧后收尾
            q.put(("error", {"message": str(exc)}))
            q.put(None)
            return
        q.put(("done", {**result, "llm_mode": bool(llm.available())}))
        q.put(None)  # 收尾哨兵

    thread = threading.Thread(target=worker)
    thread.start()

    async def gen() -> AsyncIterator[str]:
        while True:
            item = await asyncio.get_event_loop().run_in_executor(None, q.get)
            if item is None:
                break
            yield _event(*item)
        thread.join(timeout=120)

    return StreamingResponse(gen(), media_type="text/event-stream")


class AguiRequest(BaseModel):
    """兼容直连调用（非 @ag-ui/client）时的简化入参。"""
    alert_signature: str
    entity_id: str = ""


@app.post("/api/diagnosis/agui/simple")
async def agui_diagnosis_simple(req: AguiRequest) -> StreamingResponse:
    """AG-UI 端点的简化入参形态（signature 直传）。"""
    return await agui_diagnosis({"state": {
        "alert_signature": req.alert_signature, "entity_id": req.entity_id}})


@app.post("/api/diagnosis/agui")
async def agui_diagnosis(input_data: dict) -> StreamingResponse:
    """AG-UI 协议端点（新页面试点）：探索式诊断 → AG-UI 1.0 标准事件流。

    标准入参 RunAgentInput（@ag-ui/client 的 HttpAgent 自动生成）：
    thread_id/run_id 回传（客户端会话语义）、state{alert_signature,
    entity_id} 或末条 user 消息作为告警输入。事件映射：RunStarted →
    每个工具步 ToolCall 四件套（Start/Args/End/Result）+ CUSTOM
    topo_action（拓扑点亮数据源）→ CUSTOM pruned（剪枝）→ 结论
    TextMessage 三件套 → RunFinished。规则/LLM 模式同构。
    """
    import asyncio
    import uuid

    from ag_ui.core import (
        PROTOCOL_VERSION,
        CustomEvent,
        RunFinishedEvent,
        RunStartedEvent,
        TextMessageContentEvent,
        TextMessageEndEvent,
        TextMessageStartEvent,
        ToolCallArgsEvent,
        ToolCallEndEvent,
        ToolCallResultEvent,
        ToolCallStartEvent,
    )
    from ag_ui.encoder import EventEncoder

    from diagnosis.graphs.explore import run_explore

    thread_id = input_data.get("threadId") or input_data.get("thread_id") or str(uuid.uuid4())
    run_id = input_data.get("runId") or input_data.get("run_id") or str(uuid.uuid4())
    state = input_data.get("state") or {}
    messages = input_data.get("messages") or []
    signature = (state.get("alert_signature")
                 or next((m.get("content") for m in reversed(messages)
                           if m.get("role") == "user"), ""))
    entity_id = state.get("entity_id", "")

    okb = OkbClient()
    llm = LlmClient()
    enc = EventEncoder(accept="text/event-stream")

    # 共享队列：worker 推帧、gen 消费——emit 到达即送下游（真流式）
    q: "queue.Queue[str | None]" = queue.Queue()
    q.put(enc.encode(RunStartedEvent(
        thread_id=thread_id, run_id=run_id, protocol_version=PROTOCOL_VERSION)))

    def worker() -> None:
        def emit(ev: str, payload: dict) -> None:
            if ev != "step":
                if ev == "error":
                    q.put(enc.encode(CustomEvent(
                        name="error", value={"message": payload.get("message", "")})))
                return
            tool, idx = payload["tool"], payload["index"]
            if tool == "degraded":
                q.put(enc.encode(CustomEvent(name="degraded", value=payload.get("result", {}))))
                return
            tc_id = f"tc-{idx}"
            q.put(enc.encode(ToolCallStartEvent(tool_call_id=tc_id, tool_call_name=tool)))
            q.put(enc.encode(ToolCallArgsEvent(
                tool_call_id=tc_id,
                delta=json.dumps(payload.get("args", {}), ensure_ascii=False))))
            q.put(enc.encode(ToolCallEndEvent(tool_call_id=tc_id)))
            q.put(enc.encode(ToolCallResultEvent(
                tool_call_id=tc_id, message_id=f"tmr-{idx}", role="tool",
                content=json.dumps(payload.get("result", {}), ensure_ascii=False)[:4000])))
            topo = payload.get("topo") or {}
            if topo:
                q.put(enc.encode(CustomEvent(name="topo_action", value=topo)))

        try:
            final = run_explore(signature, entity_id, okb, llm, emit=emit)
        except Exception as exc:  # noqa: BLE001 - 异常转 error 帧后收尾
            q.put(enc.encode(CustomEvent(name="error", value={"message": str(exc)})))
            q.put(None)
            return
        if final.get("pruned"):
            q.put(enc.encode(CustomEvent(name="pruned", value=final["pruned"])))
        # 结论拓扑：结构化传播链（起始码/首轮全链/剪枝分支/取证事实）——前端图渲染数据源
        q.put(enc.encode(CustomEvent(name="conclusion_graph", value={
            "code": final.get("code", ""), "chain": final.get("chain", []),
            "pruned": final.get("pruned", []), "facts": final.get("facts", {})})))
        msg_id = str(uuid.uuid4())
        summary = final.get("conclusion", {}).get("summary", "")
        q.put(enc.encode(TextMessageStartEvent(message_id=msg_id, role="assistant")))
        q.put(enc.encode(TextMessageContentEvent(message_id=msg_id, delta=summary)))
        q.put(enc.encode(TextMessageEndEvent(message_id=msg_id)))
        q.put(enc.encode(RunFinishedEvent(thread_id=thread_id, run_id=run_id)))
        q.put(None)  # 收尾哨兵

    thread = threading.Thread(target=worker)
    thread.start()

    async def gen() -> AsyncIterator[str]:
        # 队列逐帧转发：emit 即送达（LLM 多轮思考期间前端持续收到事件）
        while True:
            frame = await asyncio.get_event_loop().run_in_executor(None, q.get)
            if frame is None:
                break
            yield frame
        thread.join(timeout=120)

    return StreamingResponse(gen(), media_type="text/event-stream")


class ObservedAlert(BaseModel):
    entity: str
    stable_code: str


class ReconcileRequest(BaseModel):
    alert_signature: str
    entity_id: str                     # 必填：无实体则影响半径为空，无从对账
    observed: list[ObservedAlert]      # 实际观测到的告警（对账另一方）


@app.post("/api/diagnosis/reconcile")
async def reconcile(req: ReconcileRequest) -> dict:
    """对账：诊断影响半径（预期告警集）vs 实际观测——缺失即信号。

    matched：预期且已观测（知识成立）；missing：预期未观测（链路中断/
    知识有误/数据缺失，本身是信号）；unexpected：观测但不在预期（另有
    根因，交根因诊断）。诊断跑在 report_enabled=False 模式——对账本身
    不回写置信，回写须等人工确认对账结论。
    """
    from diagnosis.graphs.impact import reconcile_storm

    state = diagnose(req.alert_signature, entity_id=req.entity_id, report_enabled=False)
    radius = state.get("impact_radius", {})
    if not radius.get("expected_alerts"):
        return {
            "matched": [], "missing": [], "unexpected": [],
            "summary": "影响半径为空（无实体绑定或知识空白），无从对账——如实不编造",
        }
    rec = reconcile_storm(radius, [a.model_dump() for a in req.observed])
    n_m, n_x, n_u = len(rec["matched"]), len(rec["missing"]), len(rec["unexpected"])
    parts = [f"命中 {n_m} 条" + (f"/{n_m + n_x}" if n_m else "")]
    if n_x:
        parts.append(f"缺失 {n_x} 条（预期未观测：数据/知识有问题的信号，须排查）")
    if n_u:
        parts.append(f"预期外 {n_u} 条（另有根因，交根因诊断）")
    if not n_x and not n_u:
        parts.append("完全吻合")
    rec["summary"] = "；".join(parts) + "。"
    return rec


# ---------------------------------------------------------------- 诊断对话（LLM 座位第一刀）
# 最近一次诊断终态缓存（进程内存）：对话上下文注入用；会话化待 checkpointer
_LAST_DIAGNOSIS: dict = {}


class ChatRequest(BaseModel):
    question: str
    history: list[dict] = []      # [{role: "user"|"assistant", content: "..."}]


@app.post("/api/diagnosis/chat")
async def chat(req: ChatRequest) -> dict:
    """诊断对话（非流式）：对最近一次诊断追问，LLM 拿诊断终态作答。

    座位纪律（AGENTS.md）：LLM 必须可降级——未配置 OKB_LLM_BASE_URL/
    OKB_LLM_API_KEY 时 422 如实说明，不伪装模型回答；上下文由
    build_diagnosis_context 注入，LLM 不自由遍历图（证据链可审计）。
    """
    from fastapi import HTTPException

    llm = LlmClient()
    if not llm.available():
        raise HTTPException(status_code=422, detail=(
            "LLM 未配置：设置 OKB_LLM_BASE_URL / OKB_LLM_API_KEY / OKB_LLM_MODEL "
            "（OpenAI 兼容协议）后重启 8100 生效；未配置时诊断判定序不受影响（规则降级）"))
    if not _LAST_DIAGNOSIS:
        raise HTTPException(status_code=422, detail="尚无诊断结果：先跑一次诊断，再对它追问")
    try:
        context = build_diagnosis_context(_LAST_DIAGNOSIS)
        result = llm.chat(SYSTEM_PROMPT, f"诊断上下文：\n{context}\n\n追问：{req.question}",
                          history=req.history)
        return {"answer": result["content"], "reasoning": result["reasoning"],
                "context_injected": context}
    except HTTPException:
        raise
    except Exception as exc:  # noqa: BLE001 - 网关异常如实上抛
        raise HTTPException(status_code=502, detail=f"LLM 网关调用失败：{exc}") from exc


@app.post("/api/diagnosis/chat/stream")
async def chat_stream(req: ChatRequest) -> StreamingResponse:
    """诊断对话（SSE 流式）：思考过程与回答逐段推送。

    事件序列：reasoning_delta（思考逐段，思考模型）→ answer_delta
    （回答逐段）→ done（终态，含注入的上下文）→ error（网关异常）。
    未配置/无诊断结果仍 422 降级（LLM 可降级纪律）。
    """
    from fastapi import HTTPException

    llm = LlmClient()
    if not llm.available():
        raise HTTPException(status_code=422, detail=(
            "LLM 未配置：设置 OKB_LLM_BASE_URL / OKB_LLM_API_KEY / OKB_LLM_MODEL "
            "（OpenAI 兼容协议）后重启 8100 生效；未配置时诊断判定序不受影响（规则降级）"))
    if not _LAST_DIAGNOSIS:
        raise HTTPException(status_code=422, detail="尚无诊断结果：先跑一次诊断，再对它追问")

    context = build_diagnosis_context(_LAST_DIAGNOSIS)
    prompt = f"诊断上下文：\n{context}\n\n追问：{req.question}"

    async def gen() -> AsyncIterator[str]:
        try:
            for chunk in llm.chat_stream(SYSTEM_PROMPT, prompt, history=req.history):
                yield _event(f"{chunk['kind']}_delta", {"delta": chunk["delta"]})
            yield _event("done", {"context_injected": context})
        except Exception as exc:  # noqa: BLE001 - SSE 必须以 error 帧收尾
            yield _event("error", {"message": str(exc)})

    return StreamingResponse(gen(), media_type="text/event-stream")
