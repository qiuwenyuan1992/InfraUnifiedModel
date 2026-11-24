"""LLM 客户端：OpenAI 兼容协议（公司内部网关），可降级。

座位设计（既定纪律）：
- LLM 介入必须可降级——无配置（OKB_LLM_BASE_URL/OKB_LLM_API_KEY 未设）
  时 available()=False，调用方走规则降级路径，全流程仍可跑通。
- 诊断对话：LLM 拿诊断终态（结论/主链/待验证项/影响半径）作答，
  上下文由调用方注入，LLM 不自由遍历图（证据链确定性保持可审计）。
"""

from __future__ import annotations

import os
from pathlib import Path


def _load_env_file() -> None:
    """加载模块根目录 .env（不进 Git；已 export 的环境变量优先）。

    手工解析而非引入 python-dotenv：需求仅 KEY=VALUE 单行格式，
    避免为一个 12 行函数加依赖。注释（# 开头）与空行跳过，值去引号。
    """
    env_path = Path(__file__).resolve().parent.parent.parent / ".env"
    if not env_path.is_file():
        return
    for line in env_path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        key, value = key.strip(), value.strip().strip("'\"")
        if key and key not in os.environ:  # export 的环境变量优先
            os.environ[key] = value


_load_env_file()


class LlmClient:
    """OpenAI 兼容 chat 客户端（同步 httpx，对话场景无需流式首版）。"""

    def __init__(self) -> None:
        self.base_url = os.environ.get("OKB_LLM_BASE_URL", "").rstrip("/")
        self.api_key = os.environ.get("OKB_LLM_API_KEY", "")
        self.model = os.environ.get("OKB_LLM_MODEL", "gpt-4o-mini")

    def available(self) -> bool:
        return bool(self.base_url and self.api_key)

    def chat(self, system: str, user: str, history: list[dict] | None = None) -> dict:
        """单轮对话（history 为对话历史 messages，不含 system/user）。

        返回 {"content": 回答, "reasoning": 思考过程}——思考模型
        （GLM 系）的 reasoning_content 原样透传，前端折叠展示。
        """
        if not self.available():
            raise RuntimeError("LLM 未配置：OKB_LLM_BASE_URL / OKB_LLM_API_KEY 未设置")
        import json

        import httpx

        messages = [{"role": "system", "content": system}]
        for m in history or []:
            messages.append({"role": m["role"], "content": m["content"]})
        messages.append({"role": "user", "content": user})
        resp = httpx.post(
            f"{self.base_url}/chat/completions",
            headers={"Authorization": f"Bearer {self.api_key}"},
            json={"model": self.model, "messages": messages},
            timeout=120,
        )
        resp.raise_for_status()
        message = resp.json()["choices"][0]["message"]
        return {"content": message["content"], "reasoning": message.get("reasoning_content", "")}

    def chat_stream(self, system: str, user: str, history: list[dict] | None = None):
        """流式对话：yield {"kind": "reasoning"|"answer", "delta": "…"}。

        OpenAI 兼容 stream=true：逐行读 SSE data 帧，解析 delta 里的
        reasoning_content（思考模型）与 content（回答），按到达顺序透传。
        """
        if not self.available():
            raise RuntimeError("LLM 未配置：OKB_LLM_BASE_URL / OKB_LLM_API_KEY 未设置")
        import json

        import httpx

        messages = [{"role": "system", "content": system}]
        for m in history or []:
            messages.append({"role": m["role"], "content": m["content"]})
        messages.append({"role": "user", "content": user})
        with httpx.stream(
            "POST",
            f"{self.base_url}/chat/completions",
            headers={"Authorization": f"Bearer {self.api_key}"},
            json={"model": self.model, "messages": messages, "stream": True},
            timeout=120,
        ) as resp:
            resp.raise_for_status()
            for line in resp.iter_lines():
                if not line.startswith("data: "):
                    continue
                payload = line[6:].strip()
                if payload == "[DONE]":
                    break
                try:
                    delta = json.loads(payload)["choices"][0]["delta"]
                except (ValueError, KeyError, IndexError):
                    continue
                reasoning = delta.get("reasoning_content")
                if reasoning:
                    yield {"kind": "reasoning", "delta": reasoning}
                content = delta.get("content")
                if content:
                    yield {"kind": "answer", "delta": content}


# 诊断对话系统提示词：证据纪律写进 prompt（不可编造是产品命根）
SYSTEM_PROMPT = """你是基础设施故障诊断助手。你正在回答运维工程师对一次诊断的追问。

铁律：
1. 只依据下方提供的诊断上下文（结论、因果链、待验证项、影响半径、证据）作答。
2. 上下文没有的信息，明确说"诊断结果中没有这个信息"，禁止编造或外推。
3. 引用因果链时带上 stable_code 原文；引用证据时说明证据等级（draft/curated）。
4. 回答要短：先一句话直接回答，再给依据，最多三层深度。使用中文。
"""


def build_diagnosis_context(state: dict) -> str:
    """诊断终态 → LLM 上下文（结构化压缩，控制 token）。"""
    conclusion = state.get("conclusion", {})
    start_code = state.get("start_code") or conclusion.get("start_code", "")
    parts: list[str] = []
    parts.append(f"告警: {state.get('alert_signature', '')}（实体: {state.get('entity_id') or '未指定'}）")
    parts.append(f"归一异常: {start_code}")
    chain = conclusion.get("primary_chain", [])
    if chain:
        hops = " → ".join([start_code] + [h["target"] for h in chain])
        parts.append(f"主因果链: {hops}")
        levels = "、".join(f"{h['target']}({h.get('evidence_level', '?')})" for h in chain)
        parts.append(f"各跳证据等级: {levels}")
    else:
        parts.append("主因果链: 无（知识空白或叶子节点）")
    pend = state.get("pending_checks", [])
    if pend:
        parts.append("待验证检查项: " + "、".join(sorted({p["fact_key"] for p in pend})))
    peers = state.get("cross_domain_peers", [])
    if peers:
        parts.append("跨域对端提示: " + "、".join(sorted({p["other_code"] for p in peers})))
    radius = state.get("impact_radius", {})
    alerts = radius.get("expected_alerts", [])
    if alerts:
        ents = sorted({a["entity"] for a in alerts})
        parts.append(f"影响半径: {len(alerts)} 条预期告警，涉及实体 {', '.join(ents[:20])}")
    unbound = radius.get("unbound", [])
    if unbound:
        parts.append("绑定不可用（拓扑未同步）: " + "、".join(sorted({u["stable_code"] for u in unbound})))
    parts.append(f"一句话结论: {conclusion.get('summary', '')}")
    return "\n".join(parts)
