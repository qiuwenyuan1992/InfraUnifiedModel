"""InventoryPort：实例拓扑查询口（图语法，不定死实体类型）。

Fixture：双机柜 POD（升级 2026-11-23 用户要求）：
- cab-A12 / cab-B13 各 6 台服务器，每台双 PSU 双路供电（psu1 挂 A 路 PDU、
  psu2 挂 B 路 PDU）——PDU 单路故障只打掉对应侧 PSU，母线故障打掉全部
- 每柜 2 台 TOR，服务器口→TOR 口；TOR 双上联 POD 级 spine-1/2
- 空间包含：柜内设备→机柜；spine→pod

接口签名与未来真实实现（UnifiedInfraTopology REST / Nebula）一致：
power_ancestors 支持风暴根因反推（最近公共供电祖先），其余为影响半径用。
"""

from __future__ import annotations

# ---------------------------------------------------------------- fixture 数据
_SERVERS_A = [f"srv-{i:03d}" for i in range(1, 7)]     # cab-A12：001..006
_SERVERS_B = [f"srv-{i:03d}" for i in range(101, 107)]  # cab-B13：101..106
_TORS = ["tor-A1", "tor-A2", "tor-B1", "tor-B2"]
_SPINES = ["spine-1", "spine-2"]

_ENTITIES: dict[str, str] = {
    "busbar-A": "busbar", "busbar-B": "busbar",
    "pod-A": "pod",
}
for _p in ("pdu-A1", "pdu-A2", "pdu-B1", "pdu-B2"):
    _ENTITIES[_p] = "pdu"
for _cab, _servers in (("cab-A12", _SERVERS_A), ("cab-B13", _SERVERS_B)):
    _ENTITIES[_cab] = "cabinet"
    for _s in _servers:
        _ENTITIES[_s] = "server"
        _ENTITIES[f"{_s}:psu1"] = "psu"
        _ENTITIES[f"{_s}:psu2"] = "psu"
        _ENTITIES[f"{_s}:p0"] = "port"
for _t in _TORS:
    _ENTITIES[_t] = "switch"
    for _i in range(1, 4):  # 下联口 E1..E3（每 TOR 带 3 台服务器）
        _ENTITIES[f"{_t}:E{_i}"] = "port"
    _ENTITIES[f"{_t}:P1"] = "port"  # 上联口 ×2 → 双 spine
    _ENTITIES[f"{_t}:P2"] = "port"
for _sp in _SPINES:
    _ENTITIES[_sp] = "switch"
    for _i in range(1, 5):  # 下联口收 4 台 TOR 的上联
        _ENTITIES[f"{_sp}:P{_i}"] = "port"

# 供电树：busbar → PDU →（PSU → 服务器 / TOR）；A/B 路 PDU 分带本柜服务器与一台 TOR
_POWER_EDGES: set[tuple[str, str]] = {
    ("busbar-A", "pdu-A1"), ("busbar-A", "pdu-A2"),
    ("busbar-B", "pdu-B1"), ("busbar-B", "pdu-B2"),
}
for _servers, _pdu_a, _pdu_b, _tor_a, _tor_b in (
    (_SERVERS_A, "pdu-A1", "pdu-A2", "tor-A1", "tor-A2"),
    (_SERVERS_B, "pdu-B1", "pdu-B2", "tor-B1", "tor-B2"),
):
    for _s in _servers:
        _POWER_EDGES.add((_pdu_a, f"{_s}:psu1"))
        _POWER_EDGES.add((_pdu_b, f"{_s}:psu2"))
        _POWER_EDGES.add((f"{_s}:psu1", _s))
        _POWER_EDGES.add((f"{_s}:psu2", _s))
    _POWER_EDGES.add((_pdu_a, _tor_a))
    _POWER_EDGES.add((_pdu_b, _tor_b))

# 端口归属：服务器/TOR/spine → 自己的口
_OWN_EDGES: set[tuple[str, str]] = set()
for _servers in (_SERVERS_A, _SERVERS_B):
    for _s in _servers:
        _OWN_EDGES.add((_s, f"{_s}:p0"))
for _t in _TORS:
    for _i in range(1, 4):
        _OWN_EDGES.add((_t, f"{_t}:E{_i}"))
    _OWN_EDGES.add((_t, f"{_t}:P1"))
    _OWN_EDGES.add((_t, f"{_t}:P2"))
for _sp in _SPINES:
    for _i in range(1, 5):
        _OWN_EDGES.add((_sp, f"{_sp}:P{_i}"))

# 链路：服务器口 ↔ TOR 下联口；TOR 上联口 ↔ spine 下联口（双上联）
_LINK_EDGES: set[tuple[str, str]] = set()
for _servers, _tor_a, _tor_b in ((_SERVERS_A, "tor-A1", "tor-A2"), (_SERVERS_B, "tor-B1", "tor-B2")):
    for _idx, _s in enumerate(_servers, 1):
        _tor = _tor_a if _idx <= 3 else _tor_b
        _LINK_EDGES.add((f"{_s}:p0", f"{_tor}:E{(_idx - 1) % 3 + 1}"))
for _ti, _t in enumerate(_TORS, 1):
    _LINK_EDGES.add((f"{_t}:P1", f"spine-1:P{_ti}"))
    _LINK_EDGES.add((f"{_t}:P2", f"spine-2:P{_ti}"))

# 空间包含：柜内设备/服务器口 → 机柜；spine → POD
_LOC_EDGES: set[tuple[str, str]] = set()
for _cab, _servers, _tors in (
    ("cab-A12", _SERVERS_A, ("tor-A1", "tor-A2")),
    ("cab-B13", _SERVERS_B, ("tor-B1", "tor-B2")),
):
    for _e in ("pdu-A1", "pdu-A2", "pdu-B1", "pdu-B2"):
        pass  # PDU 归柜在下方按柜侧显式添加
for _servers, _cab, _pdu_a, _pdu_b, _tors in (
    (_SERVERS_A, "cab-A12", "pdu-A1", "pdu-A2", ("tor-A1", "tor-A2")),
    (_SERVERS_B, "cab-B13", "pdu-B1", "pdu-B2", ("tor-B1", "tor-B2")),
):
    for _s in _servers:
        _LOC_EDGES.add((_s, _cab))
        _LOC_EDGES.add((f"{_s}:p0", _cab))
    _LOC_EDGES.add((_pdu_a, _cab))
    _LOC_EDGES.add((_pdu_b, _cab))
    for _t in _tors:
        _LOC_EDGES.add((_t, _cab))
for _sp in _SPINES:
    _LOC_EDGES.add((_sp, "pod-A"))


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

    def power_ancestors(self, entity_id: str) -> list[tuple[str, int]]:
        """沿供电关系向上遍历（BFS），返回 [(祖先, 距离)]——风暴根因反推用。"""
        dist = {entity_id: 0}
        queue = [entity_id]
        while queue:
            cur = queue.pop(0)
            for src, dst in _POWER_EDGES:
                if dst == cur and src not in dist:
                    dist[src] = dist[cur] + 1
                    queue.append(src)
        return sorted(((e, d) for e, d in dist.items() if e != entity_id), key=lambda x: x[1])

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
        """沿空间包含关系向上找容器（机柜/POD 等任意层级）。"""
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
