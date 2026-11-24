"""合成告警数据集批量诊断：多场景闭环回归。

samples.py 的每条样本（部位+类型+时间）→ 归一 signature → 诊断 → 断言期望。
覆盖：金路径（掉电/制冷5跳/光衰/Xid）、知识如实（BGP 叶子/无出边）、未登记（人工路径）。
OKB server 未运行时跳过——不伪造通过。
"""

from __future__ import annotations

import pytest

from diagnosis.graphs.judge_graph import diagnose
from diagnosis.samples import SAMPLES, normalize
from diagnosis.tools.okb import OkbClient


@pytest.fixture(scope="module")
def okb() -> OkbClient:
    client = OkbClient()
    if not client.healthz():
        pytest.skip("OKB server 未运行（18090），批量诊断测试跳过——不伪造通过")
    return client


@pytest.mark.parametrize("sample", SAMPLES, ids=lambda s: s["id"])
def test_sample_diagnosis(sample: dict, okb: OkbClient):
    """每条合成告警跑诊断，按 expected 断言。"""
    exp = sample["expected"]

    # 归一层先验证：登记过的 (部位,类型) 必须能映射到 signature
    mapped = normalize(sample["equipment"], sample["fault_type"])
    if exp.get("unmapped"):
        assert mapped is None, f"{sample['id']} 未登记告警不应有映射"
        state = diagnose(sample["signature"], okb=okb, report_enabled=False)
        assert state["error"], "未登记必须走人工路径（error 说明）"
        assert state["conclusion"].get("status") == "unmapped_alert"
        assert state["conclusion"].get("summary"), "未登记也要有一句话结论"
        assert state["reported"] is False
        return

    assert mapped == sample["signature"], f"{sample['id']} 归一映射不一致"
    # report_enabled=False：测试不回写知识置信度（防污染——宕机边被测试
    # 跑成 curated 的教训）；判定序完整执行，仅飞轮节点跳过
    state = diagnose(sample["signature"], okb=okb, entity_id=sample.get("entity"),
                     report_enabled=False)
    assert state["conclusion"].get("summary"), f"{sample['id']} 结论必须含一句话总结"

    # 起点
    assert state["candidates"], f"{sample['id']} lookup 未命中 {sample['signature']}"
    assert state["candidates"][0]["stable_code"] == exp["start_code"]

    chains = state["trace_result"].get("chains", [])
    if exp.get("empty_chain"):
        assert not chains, f"{sample['id']} 叶子节点不应有出链（知识如实）"
        assert state["reported"] is False
        return

    if exp.get("knowledge_gap"):
        # 归一命中但无因果出边：不编造链
        assert state["conclusion"].get("primary_chain", []) == []
        assert state["reported"] is False
        return

    # 推演终点
    all_targets: list[str] = [h["target_stable_code"] for c in chains for h in c["hops"]]
    if "reaches" in exp:
        assert exp["reaches"] in all_targets, \
            f"{sample['id']} 链未推进到 {exp['reaches']}，实际到达: {all_targets}"
    if "reaches_any" in exp:
        assert any(t in all_targets for t in exp["reaches_any"]), \
            f"{sample['id']} 链未命中任一 {exp['reaches_any']}，实际: {all_targets}"
    if "min_hops" in exp:
        longest = max(len(c["hops"]) for c in chains)
        assert longest >= exp["min_hops"], f"{sample['id']} 级联深度不足: {longest}"

    # 待验证检查项
    if "pending" in exp:
        facts = {pc["fact_key"] for pc in state["pending_checks"]}
        assert set(exp["pending"]) <= facts, f"{sample['id']} 待验证项缺失: {facts}"

    # 同事件对端
    if "peers_include" in exp:
        peers = {p["other_code"] for p in state["cross_domain_peers"]}
        assert exp["peers_include"] in peers, f"{sample['id']} 对端缺失: {peers}"

    # 影响半径（样本带 fixture 实体时计算：因果图×拓扑合遍历）
    if "radius_entities" in exp:
        entities = {a["entity"] for a in state["impact_radius"].get("expected_alerts", [])}
        for want in exp["radius_entities"]:
            assert want in entities, f"{sample['id']} 影响半径缺实体 {want}: {sorted(entities)}"

    # 测试模式永不回写（report_enabled=False）：飞轮只吃真实诊断
    assert state["reported"] is False, f"{sample['id']} 测试模式不应回写知识置信度"


def test_registry_covers_all_registered_samples():
    """样本自身一致性：登记样本的 signature 必须与 ALERT_REGISTRY 一致。"""
    for s in SAMPLES:
        mapped = normalize(s["equipment"], s["fault_type"])
        if not s["expected"].get("unmapped"):
            assert mapped == s["signature"]
        else:
            assert mapped is None
