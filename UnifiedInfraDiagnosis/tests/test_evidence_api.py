"""证据回链 API：evidence 原语的出口（复核偏差④）。

okb.py 的 evidence() 方法写了却零调用——页面宣称"证据可回链 OKB 出处"，
实际只有 revision_id 字符串。本端点让回链真实可点：诊断结论里的
edge_id / stable_code → OKB evidence 原语 → 出处片段列表。
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from diagnosis.api import app
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def client():
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），证据回链测试跳过")
    return TestClient(app)


def test_evidence_by_stable_code(client: TestClient):
    """按 stable_code 回链：返回出处片段（带 evidence_id 与等级）。"""
    resp = client.get("/api/diagnosis/evidence", params={"ref": "power.rack.busbar.input_lost"})
    assert resp.status_code == 200
    body = resp.json()
    assert body["items"], "必须返回出处片段"
    assert body["revision_id"], "必须钉 revision_id（可审计）"


def test_evidence_rejects_empty(client: TestClient):
    """空 ref 如实拒绝（不编造出处）。"""
    resp = client.get("/api/diagnosis/evidence", params={"ref": ""})
    assert resp.status_code == 422
