"""对账 API（预期 vs 观测）：影响半径的"缺失即信号"能力出口。

复核发现的偏差②：reconcile_storm 函数+测试早已有，但未接 API/页面——
"预期 47 台 PSU 告警、实际只来 3 条，本身是知识或数据有问题的信号"
这一定过调的能力用户看不到。本测试先固化 API 契约。
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from diagnosis.api import app


@pytest.fixture(scope="module")
def client():
    from diagnosis.tools.okb import OkbClient
    if not OkbClient().healthz():
        pytest.skip("OKB server 未运行（18090），对账 API 测试跳过")
    return TestClient(app)


def test_reconcile_reports_missing_and_unexpected(client: TestClient):
    """busbar 失电：观测 2 PDU + 1 端口 → missing 点名缺失、unexpected 点名预期外。"""
    resp = client.post("/api/diagnosis/reconcile", json={
        "alert_signature": "ALERT-BUSBAR-INPUT-LOST",
        "entity_id": "busbar-A",
        "observed": [
            {"entity": "pdu-A1", "stable_code": "power.rack.pdu.output_lost_dual"},
            {"entity": "pdu-A2", "stable_code": "power.rack.pdu.output_lost_dual"},
            {"entity": "tor-A1:E1", "stable_code": "net.port.down"},
            {"entity": "srv-101:psu1", "stable_code": "server.psu.input_lost"},  # 预期外（B柜）
        ],
    })
    assert resp.status_code == 200
    body = resp.json()
    assert ("pdu-A1", "power.rack.pdu.output_lost_dual") in [tuple(x) for x in body["matched"]]
    # 只来 1 个 TOR 口告警：其余端口 DOWN 缺失被点名（数据/知识有问题的信号）
    missing = [tuple(x) for x in body["missing"]]
    assert ("tor-A1:E2", "net.port.down") in missing
    # B 柜 srv-101 不在 busbar-A 半径内：预期外，交根因诊断
    assert ("srv-101:psu1", "server.psu.input_lost") in [tuple(x) for x in body["unexpected"]]
    assert body["summary"], "对账必须有一句话结论"


def test_reconcile_requires_entity(client: TestClient):
    """无实体：半径为空无从对账——如实拒绝，不编造。"""
    resp = client.post("/api/diagnosis/reconcile", json={
        "alert_signature": "ALERT-BUSBAR-INPUT-LOST",
        "observed": [],
    })
    assert resp.status_code == 422
