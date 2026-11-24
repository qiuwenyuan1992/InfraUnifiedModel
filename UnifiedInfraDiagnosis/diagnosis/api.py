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

from diagnosis.graphs.rack_power import build_graph, init_state
from diagnosis.tools.okb import OkbClient

app = FastAPI(title="UnifiedInfraDiagnosis API", version="0.1.0")


class DiagnoseRequest(BaseModel):
    alert_signature: str
    entity_id: str | None = None  # 告警实体：给定时计算影响半径（因果图×拓扑合遍历）
    include_draft: bool = True


def _event(name: str, payload: Any) -> str:
    """SSE 帧：event: <name>\\ndata: <json>\\n\\n。"""
    return f"event: {name}\ndata: {json.dumps(payload, ensure_ascii=False)}\n\n"


@app.post("/api/diagnosis/run")
async def run_diagnosis(req: DiagnoseRequest) -> StreamingResponse:
    """流式诊断：逐节点推送判定序执行事件，终态以 conclusion 事件结束。

    事件序列：
    - node_start  {node}                     节点开始
    - node_update {node, data}               节点产出（中间产物）
    - done        {conclusion, reported}     终态（含证据回链字段）
    - error       {message}                  未命中/异常（终态）
    """
    okb = OkbClient()
    graph = build_graph(okb)
    state = init_state(req.alert_signature, entity_id=req.entity_id)

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

    return StreamingResponse(gen(), media_type="text/event-stream")


@app.get("/api/diagnosis/healthz")
async def healthz() -> dict:
    okb = OkbClient()
    return {"status": "ok", "okb": okb.healthz()}
