"""OKB REST 客户端：四原语封装（lookup / trace / evidence / report）。

契约对齐 UnifiedInfraOKB/docs/design/api-boundaries.md v2.1。
所有响应钉 revision_id；诊断结论必须可回链（edge_id + revision_id）。
"""

from __future__ import annotations

import os

import httpx

OKB_BASE = os.environ.get("OKB_BASE_URL", "http://127.0.0.1:18090/api/v1")
OKB_TIMEOUT = float(os.environ.get("OKB_TIMEOUT", "10"))


class OkbClient:
    """OKB REST 门面。每个方法对应一个原语；网络错误向上抛，不静默吞。"""

    def __init__(self, base: str = OKB_BASE) -> None:
        self._http = httpx.Client(base_url=base, timeout=OKB_TIMEOUT)

    def lookup(self, alert_signature: str, include_draft: bool = False) -> dict:
        """告警 signature → 候选异常（含 severity/clock_hint/peers 标注）。"""
        r = self._http.get(
            "/lookup",
            params={"alert_signature": alert_signature, "include_draft": str(include_draft).lower()},
        )
        r.raise_for_status()
        return r.json()

    def trace(
        self,
        anomaly_code: str,
        max_hops: int = 6,
        top_k: int = 10,
        facts: dict | None = None,
        include_draft: bool = True,
    ) -> dict:
        """候选因果链遍历：返回 chains（逐跳条件求值）与 relations（非因果关联标注）。

        深级联场景（制冷失效类）实测需 5-6 跳，max_hops 默认取 6（场景建议值）。
        """
        params: dict = {
            "anomaly_code": anomaly_code,
            "max_hops": max_hops,
            "top_k": top_k,
            "include_draft": str(include_draft).lower(),
        }
        if facts:
            import json

            params["facts"] = json.dumps(facts)
        r = self._http.get("/trace", params=params)
        r.raise_for_status()
        return r.json()

    def evidence(self, ref: str) -> dict:
        """证据回链：按边 ID 或异常 stable_code 查出处原文。"""
        r = self._http.get("/evidence", params={"ref": ref})
        r.raise_for_status()
        return r.json()

    def report(self, diagnosis_ref: str, edge_id: str, verdict: str, notes: str = "") -> dict:
        """反馈飞轮：上报诊断使用的边与最终判定（confirmed/rejected）。幂等。"""
        r = self._http.post(
            "/report",
            json={"diagnosis_ref": diagnosis_ref, "edge_id": edge_id, "verdict": verdict, "notes": notes},
        )
        r.raise_for_status()
        return r.json()

    def healthz(self) -> bool:
        """OKB server 存活探测。"""
        try:
            # /healthz 不在 /api/v1 下：从 base 字符串截到根
            root = str(self._http.base_url).rsplit("/api", 1)[0]
            return httpx.get(f"{root}/healthz", timeout=OKB_TIMEOUT).status_code == 200
        except httpx.HTTPError:
            return False
