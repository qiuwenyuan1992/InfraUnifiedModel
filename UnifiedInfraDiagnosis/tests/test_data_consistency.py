"""数据一致性守卫：样本 ↔ fixture 拓扑 ↔ 归一表三方对账。

背景（2026-11-23 用户连续两轮抓出标签不一致：langfang-a12 / fixture-pod-a
均不在拓扑中）：样本引用的站点/实体/半径实体必须在 fixture 拓扑中真实
存在。本测试固化该约束——再造编造标签会先红在这里，而不是到页面上。

BGP 观测面（用户指出）：BGP 是逻辑会话，告警应解析到多个承载端口
（本端 + 对端），不是单实体。
"""

from __future__ import annotations

from diagnosis.graphs.impact import compute_impact_radius
from diagnosis.samples import ALERT_REGISTRY, SAMPLES
from diagnosis.tools.inventory import FixtureInventory


def _entity_ids() -> set[str]:
    return set(FixtureInventory().snapshot()["entities"])


def test_sample_sites_exist_in_topology():
    """每条样本的 site 必须是拓扑真实实体（unknown=未登记样本除外）。"""
    ids = _entity_ids()
    for s in SAMPLES:
        if s["site"] == "unknown":
            continue
        assert s["site"] in ids, f"{s['id']} site {s['site']!r} 不在拓扑实体中（编造标签）"


def test_sample_entities_exist_in_topology():
    """样本的 entity / radius_entities / surface_entities 必须真实存在。"""
    ids = _entity_ids()
    for s in SAMPLES:
        exp = s["expected"]
        if s.get("entity"):
            assert s["entity"] in ids, f"{s['id']} entity {s['entity']!r} 不在拓扑实体中"
        for key in ("radius_entities", "surface_entities"):
            for e in exp.get(key, []):
                assert e in ids, f"{s['id']} {key} 实体 {e!r} 不在拓扑实体中"


def test_registry_keys_cover_all_sample_equipment_pairs():
    """归一表 (部位,类型) 键必须覆盖全部已登记样本的组合（除 unmapped）。"""
    for s in SAMPLES:
        pair = (s["equipment"], s["fault_type"])
        if s["expected"].get("unmapped"):
            assert pair not in ALERT_REGISTRY
        else:
            assert pair in ALERT_REGISTRY, f"{s['id']} 组合 {pair} 未登记进 ALERT_REGISTRY"


def test_bgp_sample_binds_to_multiple_ports():
    """BGP 会话告警解析到多个承载端口（本端+对端），非单实体。"""
    bgp = next(s for s in SAMPLES if s["equipment"] == "net.bgp")
    assert bgp.get("entity"), "BGP 样本必须挂逻辑会话实体（如 bgp-1）"
    inv = FixtureInventory()
    radius = compute_impact_radius(
        {"chains": []},  # BGP 是因果叶子：无传播链，观测面仍须可解析
        bgp["entity"], inv, start_code=bgp["expected"]["start_code"],
    )
    surface = radius.get("surface", [])
    assert len(set(surface)) >= 2, f"BGP 观测面应含本端+对端多个端口，实际: {surface}"
    assert all(":" in e for e in surface), f"观测面应为端口实体，实际: {surface}"
