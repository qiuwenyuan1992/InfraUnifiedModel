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

import json
from typing import Any, AsyncIterator

from fastapi import FastAPI
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from diagnosis.graphs.judge_graph import build_graph, diagnose, init_state
from diagnosis.graphs.storm_graph import build_storm_graph, init_storm_state
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
    graph = build_graph(okb)
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
async def storm_diagnosis(req: StormRequest) -> dict:
    """告警风暴根因反推：走 LangGraph 风暴判定序（四节点，可流式）。

    响应字段与页面兼容（rationale 保留在顶层，结论细化进 conclusion）。
    """
    okb = OkbClient()
    graph = build_storm_graph(okb)
    result = graph.invoke(init_storm_state(
        [{"entity": a.entity, "stable_code": a.stable_code} for a in req.alerts]))
    root = result.get("root_cause")
    conclusion = result.get("conclusion", {})
    return {
        "independent": result["independent"],
        "root_cause": {**root, "verify": result["verify"]} if root else None,
        "affected_entities": result.get("affected_entities", []),
        "alert_count": len(result.get("affected_entities", [])),
        "rationale": conclusion.get("rationale", ""),
        "conclusion": conclusion,
    }


@app.get("/api/diagnosis/evidence")
async def evidence(ref: str) -> dict:
    """证据回链：edge_id / stable_code → OKB 出处片段（可审计）。"""
    if not ref.strip():
        from fastapi import HTTPException
        raise HTTPException(status_code=422, detail="ref 不能为空")
    return OkbClient().evidence(ref)


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
    """诊断对话：对最近一次诊断追问，LLM 拿诊断终态（结论/链/待验证/半径）作答。

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
        answer = llm.chat(SYSTEM_PROMPT, f"诊断上下文：\n{context}\n\n追问：{req.question}",
                          history=req.history)
        return {"answer": answer, "context_injected": context}
    except HTTPException:
        raise
    except Exception as exc:  # noqa: BLE001 - 网关异常如实上抛
        raise HTTPException(status_code=502, detail=f"LLM 网关调用失败：{exc}") from exc
