"""InventoryPort：实例拓扑查询口（图语法，不定死实体类型）。

设计（2026-11-23 用户确认）：
- 接口按查询动词定义（供电可达/链路对端/空间包含），不限定机柜或任何具体
  实体——拓扑同步铺到哪，诊断半径自动扩到哪；查不到如实返回空，不特判领域。
- 当前实现为 FixtureInventory（代码内固定小 POD）：busbar→PDU→服务器/TOR
  供电树 + 服务器口↔TOR 口链路 + 机柜空间包含。真实实现将来对接
  UnifiedInfraTopology 的 REST 接口（其 Nebula 已有 power_upstream /
  links_to / 空间包含），接口签名不变，判定序零改动。
- 制冷域实体尚未同步：影响半径绑定层如实标 binding_unavailable，等同步到位
  自动恢复（用户确认：知识已预留，不特制限制）。
"""

from __future__ import annotations

# ---------------------------------------------------------------- fixture 数据
# 假 POD：A12 机柜，双 PDU 供电 4 台服务器 + 1 台 TOR，服务器口↔TOR 口直连
_SERVERS = ["srv-001", "srv-002", "srv-003", "srv-004"]

_ENTITIES: dict[str, str] = {
    "busbar-A": "busbar",
    "pdu-A": "pdu",
    "pdu-B": "pdu",
    "tor-A": "switch",
    "cab-A12": "cabinet",
}
for _s in _SERVERS:
    _ENTITIES[_s] = "server"
    _ENTITIES[f"{_s}:p0"] = "port"
    _ENTITIES[f"tor-A:E{_SERVERS.index(_s) + 1}"] = "port"

# 供电树：busbar → PDU →（服务器 / TOR），双路冗余
_POWER_EDGES: set[tuple[str, str]] = {("busbar-A", "pdu-A"), ("busbar-A", "pdu-B"), ("pdu-A", "tor-A")}
_POWER_EDGES |= {(p, s) for p in ("pdu-A", "pdu-B") for s in _SERVERS}

# 端口归属与链路：srv 口 ↔ TOR 口（两端都会看到 link 告警）
_OWN_EDGES: set[tuple[str, str]] = {(s, f"{s}:p0") for s in _SERVERS} | {
    ("tor-A", f"tor-A:E{i + 1}") for i in range(len(_SERVERS))
}
_LINK_EDGES: set[tuple[str, str]] = {(f"{_s}:p0", f"tor-A:E{i + 1}") for i, _s in enumerate(_SERVERS)}

# 空间包含：设备 → 机柜
_LOC_EDGES: set[tuple[str, str]] = {(e, "cab-A12") for e, t in _ENTITIES.items() if t in ("pdu", "server", "switch")}


class FixtureInventory:
    """InventoryPort 的 fixture 实现：内存静态图，接口与未来真实实现一致。"""

    def entity_type(self, entity_id: str) -> str | None:
        return _ENTITIES.get(entity_id)

    def power_scope(self, frontier: set[str], entity_type: str | None = None) -> list[str]:
        """沿供电关系向下游遍历，返回（可选按类型过滤的）受影响实体集。"""
        seen: set[str] = set(frontier)
        queue = list(frontier)
        while queue:
            cur = queue.pop()
            for src, dst in _POWER_EDGES:
                if src == cur and dst not in seen:
                    seen.add(dst)
                    queue.append(dst)
        out = {e for e in seen if e in _ENTITIES}
        if entity_type:
            out = {e for e in out if _ENTITIES[e] == entity_type}
        return sorted(out)

    def link_endpoints(self, servers: list[str]) -> list[str]:
        """服务器集 → 其端口及链路对端（本端+对端都会观测到链路异常）。"""
        ports: set[str] = set()
        for s in servers:
            for owner, port in _OWN_EDGES:
                if owner == s:
                    ports.add(port)
                    for a, b in _LINK_EDGES:
                        if a == port:
                            ports.add(b)
                        elif b == port:
                            ports.add(a)
        return sorted(ports)

    def containers(self, frontier: set[str], entity_type: str) -> list[str]:
        """沿空间包含关系向上找容器（机柜/房间等任意层级）。"""
        found: set[str] = set()
        for e in frontier:
            for src, dst in _LOC_EDGES:
                if src == e and _ENTITIES.get(dst) == entity_type:
                    found.add(dst)
        return sorted(found)

    def snapshot(self) -> dict:
        """fixture 拓扑快照：实体 + 三类连接关系（测试页展示机柜连接用）。"""
        return {
            "entities": dict(sorted(_ENTITIES.items())),
            "power_edges": [list(e) for e in sorted(_POWER_EDGES)],
            "link_edges": [list(e) for e in sorted(_LINK_EDGES)],
            "containment_edges": [list(e) for e in sorted(_LOC_EDGES)],
        }
