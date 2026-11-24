"""影响半径：因果图 × 拓扑图交替遍历（工作清单算法）。

核心：OKB trace 回答"会发生什么"（类型层），InventoryPort 回答"发生在谁
身上"（实例层）。每跳把因果边的目标异常绑定到实体集——结论从两图遍历中
涌现（如"busbar-A 失电 → 2 PDU、4 服务器、4 条 TOR 端口告警预期"），
没有任何规则手写过完整结论。

绑定规则当前为确定性规则表（LLM 座位的降级实现）：将来由 LLM 读边语义
选择展开关系（产出查询指令），实体展开仍由引擎执行，证据链保持可回归。
绑定不可用（如制冷实体未同步）如实标记，不编造。
"""

from __future__ import annotations

from typing import Any

from diagnosis.tools.inventory import FixtureInventory


def bind_entities(frontier: set[str], target_code: str, inventory: FixtureInventory) -> list[str] | None:
    """绑定：该目标异常会发生在前沿实体集的哪些实体上；None=绑定不可用。"""
    if target_code.startswith("cooling."):
        # 制冷域实体拓扑尚未同步：如实降级（等同步到位自动恢复）
        return None
    if target_code.startswith(("server.", "storage.")):
        # 服务器侧异常：供电可达的服务器（含起点自身）
        return inventory.power_scope(frontier, entity_type="server")
    if target_code.startswith("net."):
        # 网络侧异常：受影响服务器的端口及链路对端（本端+对端）。
        # 起点本身就是端口/交换机实体（不在供电树）时，power_scope 为空——
        # 直接沿链路展开起点自身的端口与对端。
        servers = inventory.power_scope(frontier, entity_type="server")
        if servers:
            return inventory.link_endpoints(servers)
        # 端口归属宿主：srv-001:p0 → srv-001；宿主再展链路
        owners = {e.split(":")[0] for e in frontier}
        servers = inventory.power_scope(owners, entity_type="server")
        if servers:
            return inventory.link_endpoints(servers)
        return inventory.link_endpoints(sorted(frontier))
    if target_code.startswith("env."):
        # 环境侧异常：所在空间容器（房间等，层级由拓扑决定）
        return inventory.containers(frontier, entity_type="room")
    if target_code.startswith("power."):
        parts = target_code.split(".")
        # power.<device>.<fault> → 设备类型；power.rack.<device>.<fault> 再取一层
        etype = parts[1] if len(parts) > 1 else None
        if etype == "rack" and len(parts) > 2:
            etype = parts[2]
        if etype == "cabinet":
            # 机柜级异常：受影响实体所在的机柜容器
            return inventory.containers(frontier, entity_type="cabinet")
        return inventory.power_scope(frontier, entity_type=etype)
    return None


def compute_impact_radius(trace_result: dict, start_entity: str, inventory: FixtureInventory) -> dict:
    """沿 trace 因果链逐跳绑定实体，产出预期告警集（影响半径）。

    预期≠必发：条件未验证的跳同样进入预期集（可能性由告警风暴对账处理）。
    每条预期告警携带 edge_id/revision_id 证据回链；多条链共享前缀时
    按 (entity, stable_code) 去重——同一预期告警只保留一条证据。
    """
    expected: list[dict] = []
    seen: set[tuple[str, str]] = set()
    unbound: list[dict] = []
    for chain in trace_result.get("chains", []):
        frontier = {start_entity}
        for hop in chain.get("hops", []):
            target = hop["target_stable_code"]
            bound = bind_entities(frontier, target, inventory)
            if bound is None:
                unbound.append({
                    "stable_code": target,
                    "reason": "binding_unavailable",
                    "edge_id": hop.get("edge_id", ""),
                })
                continue  # 前沿不动：绑定不可用的跳不展开，如实记录
            frontier = set(bound)
            for entity in bound:
                key = (entity, target)
                if key in seen:
                    continue
                seen.add(key)
                expected.append({
                    "entity": entity,
                    "stable_code": target,
                    "edge_id": hop.get("edge_id", ""),
                    "revision_id": hop.get("revision_id", ""),
                })
    return {"start_entity": start_entity, "expected_alerts": expected, "unbound": unbound}


def reconcile_storm(radius: dict, observed: list[dict]) -> dict:
    """告警风暴对账：预期集 vs 实际观测告警 (entity, stable_code)。

    - matched：预期且已观测（知识成立、数据到位）
    - missing：预期未观测——可能链路中断/知识有误/数据缺失，本身是信号
    - unexpected：观测但不在预期——可能另有根因，交根因诊断
    """
    exp = {(a["entity"], a["stable_code"]) for a in radius.get("expected_alerts", [])}
    obs = {(o["entity"], o["stable_code"]) for o in observed}
    matched = sorted(exp & obs)
    missing = sorted(exp - obs)
    unexpected = sorted(obs - exp)
    return {"matched": matched, "missing": missing, "unexpected": unexpected}
