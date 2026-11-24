"""告警风暴根因反推：多条同型告警 → 最近公共供电祖先 → 疑似上层故障。

用户场景（2026-11-23）：同机柜多台服务器同时报 PSU 输入丢失，诊断应指向
上层 PDU/母线故障——从观测反推根因（与影响半径的"从根因推影响"互为对偶）。

算法：收集同型告警实体 → 各自沿供电关系向上找祖先 → 交集为共同供电点 →
取距离最近者（最近公共供电祖先）为疑似根因。结论与 OKB 知识交叉验证
（trace 可达 / same_event 对端），验证不过只标 topology_inferred 不编造。
"""

from __future__ import annotations

from diagnosis.tools.inventory import FixtureInventory

# 祖先实体类型 → 疑似根因异常类型（验证步会对 OKB 核实，不凭空断言）
_ROOT_CODE_BY_TYPE = {
    "pdu": "power.rack.pdu.output_lost_dual",
    "busbar": "power.rack.busbar.input_lost",
}


def _verify_against_okb(okb, alert_code: str, root_code: str) -> str:
    """知识验证：根因能 trace 到告警，或告警的 same_event 对端含根因。"""
    if okb is None:
        return "unverified"
    try:
        tr = okb.trace(root_code, max_hops=6, include_draft=True)
        targets = {h["target_stable_code"] for c in tr.get("chains", []) for h in c.get("hops", [])}
        if alert_code in targets:
            return "knowledge_backed"
        tr2 = okb.trace(alert_code, max_hops=3, include_draft=True)
        peers = {r["other_stable_code"] for r in tr2.get("relations", []) if r.get("edge_kind") == "same_event"}
        if root_code in peers:
            return "knowledge_backed"
    except Exception:  # noqa: BLE001 - OKB 不可达时如实降级为拓扑推断
        pass
    return "topology_inferred"


def infer_power_root(alerts: list[dict], inventory: FixtureInventory, okb=None) -> dict:
    """同型 PSU 告警风暴 → 最近公共供电祖先 = 疑似根因。"""
    psu = [a for a in alerts if a.get("stable_code") == "server.psu.input_lost"]
    if len(psu) < 2:
        return {
            "independent": True, "root_cause": None,
            "affected_entities": [a.get("entity", "") for a in alerts],
            "rationale": "同型告警不足 2 条，无共同供电点可反推（单点告警走正向诊断）",
        }

    entities = [a["entity"] for a in psu]
    chains = {e: dict(inventory.power_ancestors(e)) for e in entities}
    common: set[str] = set.intersection(*[set(c) for c in chains.values()])
    if not common:
        return {
            "independent": True, "root_cause": None,
            "affected_entities": entities,
            "rationale": "告警实体无共同供电点——跨供电域的独立故障，非单一上层根因",
        }

    # 最近公共祖先：在所有链上距离最小者
    root_entity = min(common, key=lambda e: min(c[e] for c in chains.values()))
    root_type = inventory.entity_type(root_entity)
    root_code = _ROOT_CODE_BY_TYPE.get(root_type or "")
    if not root_code:
        return {
            "independent": True, "root_cause": None,
            "affected_entities": entities,
            "rationale": f"共同供电点 {root_entity}（类型 {root_type}）无对应异常类型映射，疑似根因未入库",
        }

    distance = min(c[root_entity] for c in chains.values())
    verify = _verify_against_okb(okb, "server.psu.input_lost", root_code)
    return {
        "independent": False,
        "root_cause": {
            "entity": root_entity,
            "entity_type": root_type,
            "stable_code": root_code,
            "verify": verify,
        },
        "affected_entities": entities,
        "alert_count": len(entities),
        "rationale": (
            f"{len(entities)} 条 PSU 输入丢失告警的共同供电点是 {root_entity}"
            f"（距告警实体 {distance} 跳）——最近公共供电祖先为疑似上层故障；"
            + ("OKB 因果图验证通过（根因可 trace 到告警）" if verify == "knowledge_backed"
               else "拓扑推断（OKB 未覆盖该因果路径，知识空白如实标注）")
        ),
    }
