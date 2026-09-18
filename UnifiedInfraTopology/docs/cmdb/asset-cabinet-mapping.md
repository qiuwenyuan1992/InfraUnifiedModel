# CMDB 资产、端口与供电链路：阶段性模型总结

状态：已完成现有接口文档与样例的逐项校对；仍含明确列出的待确认项，不是已实施的 Go/API/Nebula 契约。

## 本阶段结论速览

- 只建设 `UnifiedInfraTopology`；`topology_grid` 及其数据库只读参考，不做修改。
- 设备、网络端口、GPU 卡及 GPU 上联资料均已提供，不再索要同类资料；GPU 关联网卡端口暂作属性，不建立网卡实体。
- 设备统一以永久不变且不复用的 `device_sn` 标识身份并关联关系；`device_view` 顶层 `uuid` 会变化，不进入设备模型。网络设备稳定的 `device_uuid` 仅用于端口归属解析。其他资源仍按各对象已确认的稳定 UUID 建模，端口使用 `port_uuid`。
- 建立轻量 `data_center` 和统一 `pod` 节点。机柜通过来源 IDC 引用关联数据中心；设备通过管理面 `pod_uuid/pod_id` 或计算面 `compute_plane[].pod_id` 关联同一类 POD。楼栋、房间及模组暂保留稳定引用和展示字段，不扩展为独立节点。供电关系范围到变压器为止。
- 端口组先作为端口属性支持组合筛选，不单独建立组节点；链路级标识、本端成员标识及三层分类分别保留，不与 LAG 混同。
- 直接通过 CMDB API 定期分页全量同步：默认每天一次、5 并发、每页 `20000`，均作为配置；接受时间差，不要求一致性快照。分页统一，`idc_id` / `idc` 等业务字段按对象映射。
- 节点和关系记录创建时间与本轮刷新标记；整轮全量采集、关系处理、写入成功后才按受管范围清理旧标记数据。失败不清理；字段语义不明确时保留原值。
- 本文记录决策，不代表已完成采集、发布或 schema 改造。允许重建本项目开发空间，但实际删除重建前仍需确认目标。

## 1. 范围与依据

- [设备接口原始资料](device_view.md)：`POST /pub/api/v3/find/instance/object/device_view`。
- [机柜接口原始资料](space_cainet.md)：`POST /pub/api/v3/find/instance/object/cabinet`。
- [数据中心接口原始资料](space_idc.md)：`POST /pub/api/v3/find/instance/object/idc`。
- [POD 接口原始资料](space_pod.md)：`POST /pub/api/v3/find/instance/object/pod`。
- [设备管理面 POD 关联样例](device_view_demo.json)：覆盖管理面 `pod_uuid/pod_id/pod_name/plane` 与设备基础属性。
- 原始资料保留，不改写样例，不把此文作为完整 CMDB 数据字典。
- 本文只整理本项目；`topology_grid` 仅供只读参考，不修改其代码、配置或数据库。
- [列头柜接口](space_rpp.md)：`POST /pub/api/v3/find/instance/object/idc_RPP`。
- [UPS 组接口](space_ups_group.md)：`POST /pub/api/v3/find/instance/object/idc_ups_group`。
- [UPS 设备接口](space_ups.md)：`POST /pub/api/v3/find/instance/object/idc_ups`。
- [变压器接口](space_transformer.md)：`POST /pub/api/v3/find/instance/object/idc_transformer`。
- [网络设备端口接口](device_network_port.md)：`POST /pub/api/v3/find/instance/object/port_view`。
- [GPU 卡及计算面 POD 设备样例](device_server_gpu.md)：`POST /pub/api/v3/find/instance/object/server_gpu`；设备部分通过 `compute_plane[].pod_id=2289` 验证计算面归属。
- [GPU 上联接口](device_server_gpu_uplink.md)：`POST /pub/api/v3/find/instance/object/gpu_uplink`。
- 已确认采用 CMDB API 同步上述对象，不新增 CMDB MySQL/MongoDB 直读实现。历史项目的数据访问方式不约束本项目模型。
- 本次不修改建表脚本、不执行数据库操作，也不实现采集和发布。

本文使用三种标记：**已确认**（用户明确说明或接口说明）、**样例验证**（仅由当前样例证明）、**建议/待确认**（不能当作接口保证）。用户明确确认的业务规则优先于旧接口文档中的复制说明；两者冲突时必须在第 7 节保留冲突记录，不能用任一方静默覆盖另一方。

## 2. 已确认的建模边界

1. 本项目按已确认的接入范围将 `device_view` 作为统一设备入口；当前样例只验证其同时包含服务器和网络设备，不能仅据六条样例证明覆盖所有设备类型。
2. 用户确认：所有设备的 `device_sn` 唯一、永久不变且退役后不复用，统一作为最终设备身份。`inst_id` 用于查询和交叉校验。`device_view` 顶层 `uuid` 会变化，不进入领域模型、业务存储或图节点；网络设备稳定的 `device_uuid` 仅作为端口归属解析别名。
3. 设备大类、子类和角色分别表达，不能因统一设备实体而丢失分类。
4. 机柜作为设备位置和供电关系的连接实体；数据中心建立轻量图节点，POD 建立统一逻辑归属节点，楼栋、房间和模组暂不建立独立图节点。
5. 机柜接口补充机柜属性，本项目决定使用来源 UUID 与设备关联，不以名称作为永久身份；当前单份机柜样例不能独立证明 UUID 跨时间不变。
6. ARP/LLDP 是网络发现证据，不等于 CMDB 资产登记；不能把每个 ARP 端点直接当作服务器资产。
7. 不将所有 CMDB 字段搬进 Nebula；本阶段列出需保留的信息，具体存储位置后续确定。

## 3. 设备字段映射

| CMDB 字段 | 含义及整理规则 | 状态 |
|---|---|---|
| `uuid` | `device_view` 顶层记录 UUID 会变化；不进入设备领域模型、业务存储或图节点，也不参与身份、关系或冲突判断 | 已确认 |
| `inst_id` | 保留为 CMDB 实例标识，辅助查询和一致性核对，不作为最终设备身份 | 已确认 |
| `device_sn` | 所有设备统一使用的最终身份；唯一、永久不变且退役后不复用 | 已确认 |
| `device_uuid` | 服务器为空；网络设备中固定不变，作为 `local_device_uuid` 解析端口归属的别名，最终仍解析到 `device_sn` | 已确认 |
| `device_id` | 样例中与 `inst_id` 相同，但不推定所有记录恒等 | 待确认 |
| `parent_type` / `parent_type_id` | 资产大类，例如服务器、网络设备；保留来源 ID 与名称，规范分类映射待定 | 样例验证 |
| `device_type` / `device_type_id` | 子类，例如通用服务器、盒式交换机，不与大类混用 | 样例验证 |
| `role` | 角色，例如网络设备的 `T1`，不是资产大类 | 样例验证 |
| `device_name` / `host_name` | 设备名与主机名分别保留；显示名称优先级另定 | 建议 |
| `service_status` / `service_status_id` | 服务状态；不能不经映射就视为资产生命周期或删除状态 | 待确认 |
| `cabinet_uuid` / `idc_cabinet_id` / `cabinet` | 机柜 UUID、实例引用和展示名称 | 样例验证 |
| `building_uuid` / `building_id` 及名称字段 | 楼栋引用及展示信息 | 字段存在；完整关系待空间接口确认 |
| `room_uuid` / `room_id` / `room` | 房间引用及展示信息 | 字段存在；完整关系待空间接口确认 |
| `idc_id` / `idc` / `idc_module_id` | IDC 与模组引用；不要把模组和楼栋直接合并 | 待空间接口确认 |
| `u_position` | 设备在机柜中的位置，如 `11--12`；先保留原值，区间解析规则待确认 | 样例验证 |
| `height` | 设备高度，与机柜总 U 数不同 | 样例及业务说明 |
| `pod_uuid` / `pod_id` / `pod_name` / `plane` | 管理面 POD 归属；优先通过 UUID 解析，数字 ID 与名称用于交叉校验 | 样例与 POD API 已验证 |
| `compute_plane[]` / `compute_pod_id[]` / `compute_pod_name[]` | 计算面 POD 多值归属；`compute_plane[].pod_id` 解析 `pod.inst_id`，其余数组用于核对 | 样例与 POD API 已验证 |
| `logic_idc_uuid` / `logic_idc_id` | 逻辑 IDC 引用；当前不据此推定 POD 到物理数据中心的关系 | 字段存在，关系待确认 |
| `device_ip_info`、`eth_ip`、`ilo_ip`、`management_ip` 等地址字段 | 规范化后作为设备内嵌属性；`device_ip_info` 为主来源，顶层重复字段仅补全和校验；IP 不作为设备永久身份 | 已确认 |
| 负责人、部门、业务、应用等 | 按查询需要保留管理属性，不默认全部建立图节点 | 建议 |

来源主标识应包含 CMDB 来源命名空间和对象类型。设备身份键为 `source_id:device:device_sn`；`inst_id` 按对象类型解析，不能把不同对象类型下的相同数字视为同一实体。

设备关系端点统一使用 `device_sn`。网络端口的设备别名 UUID 先解析到网络设备 `device_sn`；机柜、POD、供电对象等其他资源继续使用各自已确认的稳定 UUID。只有数字 ID 的非设备引用按目标类型查 `inst_id → 稳定身份` 映射后关联。

六条 `device_view_demo.json` 已核对：`device_ip_info.eth/ilo` 与顶层 `eth_ip*`、`ilo_ip*` 重复，且前者同时保留网关、掩码、MAC、端口和 VLAN 信息。设备地址以 `device_ip_info` 为主来源，顶层地址字段只在主来源缺失时补全，并用于一致性校验；空 IP 不写入。

领域模型和 API 将有效地址规范化为 `device_ip_info[]`，每项保留 `type`、`purpose`、`ip`、`gateway`、`mask`、`mac`、`port`、`vlan_id`，同时在设备上维护去重后的 `all_ips[]`。例如服务器带外地址由 `device_ip_info.ilo[].ip` 规范为 `purpose=out_of_band`；输入该 IP 可以反查设备 `device_sn`。为保证精确 IP 查询效率，MySQL 维护 `IP → resource_kind/resource_identity/purpose` 的辅助倒排索引；设备的 `resource_identity` 为 `device_sn`。该索引不是图节点，IP 也不作为设备身份。

`device_ip_info.*.port` 可以为空；不能仅凭这些记录构造完整接口、聚合成员或物理连线。普通服务器上联改由 `server_tor_ports` 建立：先以 `sn` 解析 ToR 设备，再在该设备范围内以 `ports[]` 匹配 `port_view.port_name`，最终形成 `device → interface` 的 `server_uplink` 关系。该来源没有服务器本端端口身份，因此不建立服务器端口节点。

## 4. 设备与机柜的样例关联

当前提供的六条设备记录与机柜样例可关联：

| 设备字段 → 机柜字段 | 样例值 |
|---|---|
| `cabinet_uuid` → `uuid` | `c0b7b4f5-c5b2-ab3d-af4e-da3fbe7c055b` |
| `idc_cabinet_id` → `inst_id` | `62897` |
| `cabinet` → `code` | `M1-F2-IT06-R08-16` |
| `building_id` → `phy_building_id` | `404` |
| `room_id` → `phy_room_id` | `889` |
| `idc_id` → `idc_id` | `451` |
| `idc_module_id` → `idc_module_id` | `100` |

样例为 5 台服务器、1 台网络设备，与机柜 `server_num=5`、`network_device_num=1`、`device_num=6` 一致。这是样例交叉检查，不意味着接口统计与设备分页结果始终处于同一快照。

建议以 UUID 关联、实例 ID 交叉校验，名称仅展示。UUID 与实例 ID 若指向不同机柜，应显式记录冲突，不能静默择一覆盖。

## 5. 机柜需保留的信息

以下清单包含用户标注的关注字段，以及身份、状态和关联核对所需字段；不代表全部必须作为图属性。

| 分组 | 来源字段 | 说明 |
|---|---|---|
| 身份 | `uuid`、`inst_id`、`id` | 建议 UUID 为来源主标识；机柜标识稳定性及 `id=inst_id` 的全局保证待确认 |
| 编码与展示 | `code`、`dc_colo_rack`、`isp_rack_code` | 机柜编码、建筑/房间/机架组合展示、运营商编码 |
| 空间引用 | `idc_id`、`phy_idc_id`、`phy_building_id`、`phy_room_id`、`idc_module_id`、`idc_building_structure_id` | 保留各自含义，不凭名称推定完整层级 |
| 原始引用别名 | `building_full_name`、`room_full_name` | 返回的是数字，不能当作名称；样例分别等于楼栋 ID、房间 ID |
| 行列与机架 | `idc_rows_code`、`rack`、`rack_column_code`、`rack_row`、`rack_size` | `idc_rows_code` 为机柜列编码 ID；`rack_size` 含义待确认，不当作总 U 数 |
| 容量与数量 | `u_num`、`spare_u_num`、`used_u_num`、`device_num`、`server_num`、`network_device_num` | 总 U 数、剩余/已用 U 位与设备统计；不替代设备实际归属 |
| 管理信息 | `jira_key`、`service` | 保留来源值；`jira_key` 样例为字符串；服务关联另核实 |
| 状态 | `is_delete`、`status`、`ops_status`、`power_status`、`power_state` | 删除、分配、运维、供电状态不能合并为一个生命周期枚举 |
| 类型 | `usage_type` | 0 其他、1 服务器机柜、2 网络机柜；机柜分类不限制其中只能出现单一设备类型 |
| 逻辑归属 | `pod_id`、`pod_ids` | 样例 `pod_id=0`、`pod_ids=[914]`；不能只看单值字段就认定没有 POD |
| TOR 规划 | `rack_tor_fix`、`rack_tor_inter_speed`、`rack_tor_type` | TOR 所在机柜、TOR 端口运行速率、TOR 架构 |
| 供电引用 | `row_switch_id_A`、`row_switch_id_B` | **A/B 路列头柜引用，目标为 `idc_RPP.inst_id`**；保留路别。A 路样例已验证，B 路按同类字段处理；不额外创建空开实体 |

### TOR 信息的边界

当前样例 `rack_tor_fix` 为 `M1-F2-IT06-R08-14,M1-F2-IT06-R08-15`，表示 TOR 所在机柜，不是当前机柜自身的上架位置。

- 保留原始多值信息；分隔规则、编码唯一范围和目标解析方式需确认。
- 当前先作为机柜属性；若未来建立关系，只能先表达机柜级 TOR 位置引用。
- 不能据此确定 TOR 设备 `device_sn`、端口对应、实际连线、设备数量或连线速率。
- `rack_tor_type` 的“二拖四”等字符串保留来源语义，不自行展开拓扑。

### 供电信息的边界

样例 `row_switch_id_A=6204` 对应列头柜 API 返回的 `inst_id=6204`，B 路引用为 `6114`。此前按原说明称为“空开 ID”的结论已修正：本阶段直接关联列头柜实例，不推导独立空开对象。只提供 A 路示例足以核对该映射，不要求为了样例完整性补齐 B 路全链路。

供电模型本阶段上溯到变压器为止，不继续扩展市电、馈线、发电机。设备属于机柜不代表已确定设备实际接入 A/B 哪一路；单路故障不能直接推断机柜内所有设备断电。

## 6. 空间与 POD 模型边界

本轮建立数据中心、机柜和统一 POD 节点。数据中心作为资产查询、同步范围和顶层物理归属；机柜继续连接设备位置与 A/B 路供电链路；POD 表达设备在管理面、计算面等平面中的逻辑归属。楼栋、房间、模组和逻辑 IDC 暂不建立独立节点，先保留稳定引用及展示字段。

1. 数据中心、机柜和 POD 均使用来源 UUID 建立身份；`inst_id` 仅用于来源引用解析与交叉校验。
2. 设备通过 `cabinet_uuid` 关联机柜，`idc_cabinet_id` 用于交叉校验。
3. 机柜的 `idc_id`、`phy_idc_id` 解析到 `data_center.inst_id`，最终使用两端 UUID 建立 `cabinet → data_center` 的 `located_in` 关系；两个来源字段不一致时标记冲突，不静默择一。
4. 管理面 POD 优先使用 `device.pod_uuid → pod.uuid`；同时校验 `pod_id → inst_id`、`pod_name → name`、设备 `plane=管理面` 与 `pod.plane=1`。
5. 计算面 POD 逐项使用 `device.compute_plane[].pod_id → pod.inst_id`，并通过 `compute_pod_id[]`、`compute_pod_name[]` 及 `building_id → phy_building_id` 交叉校验；数组不得只取第一项。
6. `compute_plane` 是计算面 POD 归属明细，不建立 `compute_plane` 节点。管理面与计算面引用解析到同一类 `pod` 节点，以 `pod.plane` 区分平面。
7. 同一设备、同一 POD 被多个来源字段重复引用时，只生成一条 `member_of` 关系，并合并记录来源字段；引用冲突时标记 `conflict`。
8. 设备位置属性与 `device → cabinet` 关系、机柜 IDC 引用与 `cabinet → data_center` 关系、设备 POD 引用与 `device → pod` 关系均由同一轮同步生成。
9. 位置和 POD 名称随来源更新，资源身份不因搬迁或改名变化；`u_position`、`u_start`、`u_end` 作为设备上架关系属性，`height` 仍为设备属性。
10. 未上架或未分配 POD 的设备允许缺少相应关系；引用缺失、目标未采集和来源删除必须分开记录。
11. 房间和楼宇当前只用于筛选、展示和位置一致性检查；没有房间级故障、制冷、消防或门禁关系需求前，不为其增加节点与同步成本。
12. `pod.idc_logic_id` 与设备 `logic_idc_id` 的样例值不一致，当前不生成 `pod → data_center` 或 `pod → logic_idc` 关系。

本节只确定模型边界；接口兼容、查询切换和上层空间扩展需在实现阶段完成。

## 7. 原始文档中的待核对项

- `device_view.md` 的响应样例含非法 JSON 转义，不能直接作为严格 JSON 测试夹具。本次关联校验仅在内存中规范非法转义后解析，未修改原始文档；后续接入前需取得有效 JSON 样例。
- `device_view.md` 与 `device_view_demo.json` 中六条记录的 `inst_id`、SN 和大部分业务字段可对应，但同一 `inst_id` 的顶层 `uuid` 全部不同。用户已确认该字段会变化，因此设备模型不保存它，也不再将其差异视为身份冲突。
- `device_uuid` 在服务器中为空，在网络设备中固定不变。端口样例的 `local_device_uuid` 匹配网络设备 `device_uuid=795fa397-...`；端口归属先用该别名定位网络设备，再以 `local_device_sn/device_sn` 和 `local_device_id/inst_id` 交叉校验，最终使用 `device_sn` 建立设备端关系。
- 五台服务器的 `server_tor_ports` 均有值，但其 ToR SN 和端口未出现在当前设备、端口样例中；普通服务器上联规则尚未形成样例闭环，只能保留待解析引用。
- 8 条 GPU 上联均可匹配 GPU 卡及 GPU 侧端口/槽位，但所引用的 ToR 设备和端口未提供，且 `gpu_ip` 全为空；ToR 端解析和非空 GPU IP 归属仍待补样例验证。
- 端口组旧资料把 `group_subtype=6` 列为二级分类，并写有不同一级类型调用不同接口；用户最新确认分别为三级字段和统一 `port_group` 查询。当前仅有 `group_type=3`、`port_group_link_id=2328` 的样例，其他类型需联调验证。
- 机柜请求条件仍为旧样例的 `idc_id=1`、`idc_module_id=58`、`phy_room_id=213`，不对应当前响应的 `451/100/889`。
- 机柜文档末尾关注字段清单保留旧样例值；它用于说明字段，不作为第二条实际机柜记录。
- 复杂查询中 `status` 为数字，但字段说明及响应是字符串枚举；筛选值需确认。
- `idc_id` 被描述为“机房 id”，同时存在 `phy_room_id`“房间 Id”；需用空间接口明确业务术语，不能混用。
- 字段说明写 `cabinet_num` 为服务器数量，但响应使用 `server_num`。
- `jira_key` 说明为整数，但响应为字符串。
- `temp_electrified` 重复对应“服务等级字符串”和“临时加电整数”；需纠正文档映射。
- `data.info.info.rest_a_pdu` 疑似多写一层 `info`。
- `unit_use_rule` 枚举只列 1–3，响应出现 4；不据现有枚举拒绝该值。
- 设备身份采用 `device_sn` 的决策已确认；其他资源继续使用各自已确认的稳定 UUID。机柜编码唯一范围及多值引用解析规则仍需核对。

## 8. 供电对象及关系映射

下图箭头表示来源引用或归属，不代表电流方向。UPS 组成员关系不是串联供电关系。

```text
设备 → 机柜 62897
        ├─ A路 → 列头柜 6204 → UPS组 1156 → 变压器 1588
        └─ B路 → 列头柜 6114（本次未提供该记录及其上游样例）

UPS设备 3255、3280、3370 ─成员归属→ UPS组 1156
UPS设备 3255、3280、3370 ─来源字段校验→ 变压器 1588（不建直连边）
```

| 来源字段 | 目标 | 样例及证据边界 |
|---|---|---|
| `cabinet.row_switch_id_A/B` | `idc_RPP.inst_id` | A=6204 已验证；B=6114 保留引用，按同类关系处理 |
| `idc_RPP.ups_group` | `idc_ups_group.inst_id` | 6204 → 1156 |
| `idc_RPP.transformer_group_a` | `idc_transformer.inst_id`（属性校验，不建边） | 值 1588 与变压器样例对应；保留在 RPP 节点上，用于校验经 UPS 组解析出的上游变压器 |
| `idc_ups_group.transformer_id_up` | `idc_transformer.inst_id` | 1156 → 1588 |
| `idc_ups.ups_group` | `idc_ups_group.inst_id` | 3255、3280、3370 → 1156 |
| `idc_ups.transformer_id_up` | `idc_transformer.inst_id` | 三台设备均引用 1588；与所属组上游一致 |
| `idc_transformer.standby_transformer` | 当前变压器引用的备用变压器 | 样例中变压器 1588 引用 1593；未提供目标记录，关系只能保留为未解析引用，切换条件待确认，不认定为当前供电路径 |

### RPP 变压器字段处理

`idc_RPP.transformer_group_a` 在当前样例中取值 1588，可匹配 `idc_transformer.inst_id=1588`；旧资料未给出完整字段定义，因此本文将其作为候选上游变压器引用进行校验，但不据此建立 `RPP → transformer` 图关系：

1. RPP 节点原样保留 `transformer_group_a`，不丢失 CMDB 来源信息。
2. 同步时将其解析到 `idc_transformer.inst_id`，并与 `RPP.ups_group → UPSGroup.transformer_id_up` 得到的变压器进行一致性校验。
3. 一致时仅记录校验通过；不一致时记录 `conflict`；目标未采集或不存在时记录 `unresolved`。校验结果进入同步诊断，不生成替代关系。
4. 主供电图只保留 `RPP → UPSGroup → transformer`，避免增加跨层捷径，也避免被误读为 RPP 绕过 UPS 组直接供电。
5. `transformer_group_a` 不参与供电路径遍历和故障影响范围查询；需要排查来源数据时，从 RPP 节点属性和同步诊断读取。

各已提供供电对象的 `idc=451`、`building=404`、`module=100` 一致；列头柜 `idc_room=889` 与机柜房间一致。位置一致仅作交叉校验，引用标识才是建立关系的依据。

### 最小保留字段

所有供电对象保留 `obj_id`、`uuid`、`inst_id`、`code`、`idc`、`building`、`module` 和可用的来源更新时间。按已确认决策，用资源 UUID 标识对象及最终关系；本节数字 ID 链路用于说明来源解析，不是最终存储关系键。

| 对象 | 关联与主要属性 | 说明 |
|---|---|---|
| 列头柜 `idc_RPP` | `idc_room`、`ups_group`、`transformer_group_a`、`row_in_num`、`row_power_type`、`row_rated_current`、`row_rated_voltage` | 编码 `F2_IT06AC-8A`；属性单位和枚举未说明的保留原值，不自行解释 |
| UPS组 `idc_ups_group` | `transformer_id_up`、`ups_group_num`、`ups_group_id_tag`、`ups_external_bypass` | 编码 `F2-UPS-A1`；组数量 3 与三条成员样例相符；旁路字段不等于实时旁路运行状态 |
| UPS设备 `idc_ups` | `ups_group`、`transformer_id_up`、`ups_brand`、`ups_type`、`ups_capacity`、`ups_group_id_tag` | 成员由 `ups_group` 关联，不用标记字符串关联；容量单位待确认 |
| 变压器 `idc_transformer` | `standby_transformer`、`transformer_brand`、`transformer_type`、`transformer_capacity` | 编码 `1T203`；本阶段供电链路上游边界 |

电池参数、采购和维护日期不作为本阶段关系构建的必要输入。变压器的 `up_Feeder_id`、`up_power_line_id` 等上游信息不在本阶段展开成实体或关系。

### 能力边界

- 可以规划关联查询和候选影响范围查询，但此文不代表相关查询代码已实现。
- 三台 UPS 组成一个组，不足以认定 N+1、并联容量或单机故障影响。
- 旁路、备用切换、实时状态及设备实际接电方式缺失时，不把“存在关联”输出为“必然断电”。
- 只有 UPS 组的 `transformer_id_up` 生成 `ups_group → transformer` 的 `power_upstream`；UPS 设备自身的同名字段只与所属组的上游结果做一致性校验，不建立 `ups → transformer` 直连边。若两者目标不一致，应记录冲突，不静默择一。
- CMDB `device_view` 与 `idc_ups` 等专用对象之间是否指向同一实物尚未确认；不能仅凭名称或数值 ID 合并资产。

## 9. API 同步方案与实现前约束

**已确定接入方向：直接读取上述十一类 CMDB API，不实现旧项目的数据库直读路径。**

职责建议：`internal/adapter` 处理鉴权、分页、响应解析与来源字段转换；同步 service 处理身份绑定和关系校验；worker 驱动任务；repository 承担本项目存储。当前基础 worker/CMDB adapter 仍是未实现业务的占位结构，此次不改变运行行为。

### 已确认的同步约定

- 采用定期分页全量同步；用户接受采集时间差，不要求跨页、跨接口的一致性快照，不因此阻塞实现。
- 接口使用相同分页结构：`page.start` 为偏移量，`page.limit` 为每页条数，`page.sort` 为排序字段；响应 `data.count` 为总条数，`data.info` 为本页记录。
- 每页请求量先配置为 `20000`，按 `inst_id` 升序排序，偏移量为 `0、20000、40000…`。这是客户端配置，不宣称为服务端最大限制。
- 用户确认可并发读取，CMDB 侧控制服务承载；可按返回总数规划页请求。同步周期可配置，默认每天一次；CMDB 请求并发数可配置，默认全任务合计最多 5 个在途请求，不按对象各开 5 个。
- 接受采集期间的来源变更，通过后续周期持续更新；保留身份去重和引用校验。失败或缺页不算成功；完整成功的全量轮次允许清理本轮未刷新的受管数据，不要求连续多轮缺失。
- 字段含义、速率单位、状态枚举不明确时先保留来源值，不臆造转换，也不因此阻塞基础同步。

建议的同步步骤：

1. 按配置范围分页采集各类对象，保留来源标识及类型；API `idc_id` 与 `idc` 等筛选字段按对象分别映射。
2. 设备按 `device_sn` 去重并建立 `inst_id → device_sn` 映射；网络设备额外建立 `device_uuid → device_sn` 别名索引。机柜、列头柜、UPS 组和成员等其他对象按类型建立 `inst_id → 稳定 UUID` 映射。端口的 `local_device_uuid` 先匹配 `device_view.device_uuid`，再以 `local_device_sn/device_sn`、`local_device_id/inst_id` 交叉校验，最终取得设备 `device_sn`。采集顺序不用于推断关系。
3. 保留 A/B 路属性；重复同步更新同一身份和关系，不因分页、重试或改名生成重复实体。
4. 某次采集失败、分页不完整或权限不足，不作为来源删除，也不发布为完整同步成功。跨 API 不假设存在一致快照。
5. 引用目标不在本批数据中时保留未解析状态，不伪造对象或故障结论。缺少目标可能来自采集范围，不能直接当作资产删除。
6. 由同一次业务编排维护设备位置属性及派生关系，刷新本轮标记；完成后按下述规则清理旧数据。清理失败不将整轮报告为成功。

实现时配置鉴权、同步周期和客户端并发数；默认周期和并发数已确认，不再追问。当前采用分页全量同步，不依赖增量能力，也不凭 `last_time` 等字段推定可靠增量。样例中的鉴权参数不当作生产配置，不将真实凭据写入仓库或日志。

### 全量轮次时间标记与旧数据清理

用户提出的“按时间刷新、本轮同步完成后清理旧时间数据”可直接采用，覆盖节点和关系。以下为拟定字段语义，不表示 schema 已更新：

- `created_at`：本项目首次入图库时间；已有对象后续同步不覆盖。来源创建时间使用单独字段。
- `synced_at`：本项目最后见到该对象或关系的时间。每轮开始时取得统一时间 `T`，本轮所有写入都设置为 `synced_at=T`。
- `source_updated_at`：CMDB 返回的 `last_time`、`updated_at` 等业务更新时间，仅用于展示、审计和变更判断，不用于判定对象是否已从全量结果消失。
- `sync_run_id`：可选审计字段，可关联 MySQL 中的同步任务，但不是清理条件，Nebula 节点和关系不强制保存。
- 本轮见到的所有受管节点和关系都必须刷新 `synced_at=T`，即使业务属性没有变化；只刷新新增或变化对象会误删未变化资产。

执行次序：记录统一时间 `T` → 完整采集全量 → 解析身份及关系 → 写入并刷新 `synced_at=T` → 确认所有写入完成 → 删除受管范围内 `synced_at<T` 的旧关系 → 删除允许清理的旧节点 → 报告成功。删除依据是本项目每轮维护的 `synced_at`，不是不可变的 `created_at`，也不是 CMDB 业务更新时间。

清理限制：

1. 仅限本项目独立空间、该 CMDB 来源、该同步范围和本轮完整覆盖的对象/关系类型；不得按整个空间的旧时间无差别删除。单台设备查询、临时过滤查询不是全量刷新。
2. 任一必需接口/分页失败、请求取消、写入失败或关系处理失败，则不开始旧数据清理；已写入数据留待下轮收敛，不宣称本轮具有原子快照。
3. 缺失引用可保留为未解析，但不能据此删除仍被本轮数据引用的对象，或静默删除无法重建的旧关系。相关范围无法安全判定时跳过该范围清理并记录原因。
4. 只有验证成功的完整空结果才能作为清空受管范围的依据；错误、权限不足或缺页不能伪装为空集。过滤范围缩小不代表范围外数据被删除。
5. 同一来源和重叠范围的同步轮次串行执行；默认每天一次不等于允许手动任务与定时任务并行清理。5 并发仅指同轮采集请求。
6. 先处理过期关系，再删除无其他有效归属或保留引用的过期节点；不使用会连带删除其他来源有效关系的无差别删点方式。

旧项目只读核对：`topology_grid/module/manager/standard_layer/topology/plan_topology.go` 使用轮次 `Ti`，各节点/边处理器写入后按 `c_time < Ti` 清理；现有对象重写时会刷新时间，并非只记录首次创建时间。该流程按处理器清理，部分插入错误只记录日志后仍继续清理。本项目只借鉴刷新标记思路，不照搬这些失败处理和全 Tag 清理范围。

新增来源文档注意事项：UPS 组请求示例带 `//` 注释，实际发送前需转为合法 JSON；变压器标题及部分 UPS 参数说明仍沿用“模组”措辞，按对象路径和具体字段核对，不据复制文本改变实体类型。独立请求/响应样例不要求构成完整调用记录。

## 10. 网络设备端口及端口组

### 端口身份、归属与连接

| 来源字段 | 用途 | 证据与边界 |
|---|---|---|
| `port_uuid` / `local_interface_uuid` | 端口资源身份 | 十条样例两字段一致；按此建立接口节点 |
| 顶层 `uuid` | 保留为端口视图来源记录标识 | 与 `port_uuid` 不同，不用它代替对端所引用的接口 UUID |
| `inst_id` / `port_id` / `local_interface_id` | 端口数字引用解析 | 样例一致，转换到端口 UUID，而非视图顶层 UUID |
| `device_uuid` / `local_device_uuid` | 所属网络设备的稳定来源别名 UUID | 匹配 `device_view.device_uuid`；再结合 `local_device_sn → device_view.device_sn` 和 `local_device_id → device_view.inst_id` 交叉校验，最终解析为设备 `device_sn`，不以管理 IP 作为归属键 |
| `remote_interface_uuid` / `remote_device_uuid` | 对端端口及设备引用 | `remote_interface_uuid` 解析端口；`remote_device_uuid` 按网络设备 `device_uuid → device_sn` 别名规则解析，不代表实时可达性 |
| `port_name` / `port_type` | 名称及物理/虚拟类别 | `physics`、`virtual`；Loopback、Null0、聚合接口也保留 |
| `port_speed` / `port_operation_status` | 速率及运行状态原值 | 单位和枚举未说明前不自行转换或解释 |
| `local_if_index` / `snmp_ifindex` | 接口索引 | 作为属性保留，不替代 UUID |

```text
设备 SN ─拥有→ 本端口 UUID ─CMDB连接信息→ 对端口 UUID ←拥有─ 对端设备 SN
```

对端为空时只保留端口；对端尚未采集到时保留待解析引用，不伪造完整对端资产。两端上报同一连接时去重，但不同端口对不能因设备相同而合并。

### 服务器上联不建立本端端口

`port_view` 用于建立以 `port_uuid` 标识的网络设备端口；当前十条样例中 `port_uuid=local_interface_uuid`，其全量唯一性和长期稳定性仍以接口契约为准。当前服务器样例的 `device_ip_info.*.port` 为空，`server_tor_ports` 也只提供 ToR 的 `sn`、`ports[]`、`ip`、`ip_type` 和 `as_number`，没有服务器本端端口 UUID 或名称。

```text
服务器 SN ─server_uplink→ ToR 端口 UUID ←owns_interface─ ToR 设备 SN
```

五台服务器样例均有 `server_tor_ports`，唯一网络设备样例为空。同步规则是先以 `server_tor_ports[].sn` 匹配 `device_view.device_sn`，再在已解析 ToR 设备范围内以每个 `ports[]` 值匹配 `port_view.port_name`。但是当前提供的设备与端口样例不包含这些服务器所引用的 ToR SN 和端口，因此只能验证来源字段和解析规则，不能验证已解析的 `server_uplink` 端点；本批数据应保留为 `unresolved`。关系保留来源 ToR IP、IP 类型、AS 号、端口名和解析状态；没有服务器本端端口身份时，不生成服务器 `interface` 节点，也不生成虚假的 `links_to`。

### 端口组：链路级与本端成员级分开

| 字段 | 当前采用含义 | 样例与证据边界 |
|---|---|---|
| `group_id` / `group_uuid` | 端口组链路 ID / UUID | `2328` / `4564510b-78bb-9bcb-5eaf-4e73c9ab1e39` |
| `port_group_id` / `port_group_uuid` | 本端端口组成员的 `inst_id` / UUID | `4212` / `bd30c202-4f2f-579e-64a5-5527e5d4d815` |
| `port_group_name` | 本端端口组名称 | 只作展示，不用名称判断身份 |
| `group_type` | 一级分类 | 1=专线（DCI），2=出口（POP），3=机房内端口组（inner_link） |
| `port_group_type` | 二级分类 | 样例 `13`，名称“POD上联” |
| `group_subtype` | 三级分类，按用户最新说明 | 样例 `6`；原始补充称链路子类型，不能与二级编号合并 |

**查询当前样例端口组链路的全部成员：调用 `port_group` 接口，条件 `port_group_link_id = group_id`。**样例传入 `2328`，不是本端成员 ID `4212`，并返回两条成员。用户最新说明是不再按三种一级类型调用不同接口；但旧 `device_netwok_group.md` 仍写有“不同接口”，且当前只提供 `group_type=3` 的调用样例，因此 `group_type=1/2` 的实际接口适用性仍需联调验证。

原始类型表把值 6 的“POD上联”写为二级分类；本文按用户最新确认将来源字段 `group_subtype` 作为三级字段，同时将 `port_group_type` 保留为二级字段。保持三套字段及原始编号独立，不根据同名“POD上联”推断编号等价，也不在缺少完整字典时强行推导分类父子关系。

**当前决策：端口保存组标识及分类属性，不单独建立端口组节点。**本项目可基于已同步属性做组合过滤；来源 API 当前只验证了按数字 `group_id` 查询成员，尚未验证按 `group_uuid`、`port_group_uuid` 或多字段组合查询。未来若需独立组生命周期或空组查询，再评估组实体，当前不增加该范围。

### 不与 LAG 或硬件单元混同

- 三类业务端口组不等于链路聚合。`AggregatePort101` 可以作为虚拟接口保留，但不能仅凭名称或业务分组推导其物理成员。
- `local_bond_id` 等聚合字段的语义和成员关系待具体资料确认；不影响先同步端口及已有对端引用。
- `accessory_sn`、槽位等板卡属性可保留来源信息，但不凭这份端口投影擅自创建完整硬件单元模型。

## 11. GPU 资产与上联关系

- GPU 服务器已有 `device_view` 样例：`parent_type="服务器"`、`device_type="GPU服务器"`、`device_type_id=58`。大类单独不能区分 GPU；以类型 ID 识别，名称用于展示和校验，不再要求补设备资产接口。
- GPU 卡自身以 `server_gpu.uuid` 标识；卡记录的另一个 `id` 不替代 UUID。保留 `parts_sn`、型号、显存、槽位、PCI 地址、驱动等来源属性；未明确的单位保持原值。
- API 关联先用来源字段匹配，再取得目标实体的稳定身份；设备稳定身份为 `device_sn`，GPU 卡稳定身份仍为 `server_gpu.uuid`。

| 来源匹配 | 解析结果 |
|---|---|
| `server_gpu.device_sn = device_view.device_sn` | GPU 卡所属服务器 `device_sn` |
| `gpu_uplink.device_sn = server_gpu.device_sn` 且 `gpu_uplink.gpu_sn = server_gpu.parts_sn` | 上联记录所属 GPU UUID |
| `gpu_uplink.tor_sn = device_view.device_sn` | 上联网络设备 `device_sn` |
| 已解析的上联设备 `device_sn` + `gpu_uplink.tor_port = port_view.port_name` | 上联网络端口 `port_uuid`；不能仅按端口名跨设备匹配 |

样例的 8 张卡与 8 条上联记录已核对：服务器 SN、GPU SN、GPU 侧端口名（`conn_ports` ↔ `gpu_port`）与槽位均一一对应。当前样例未提供这 8 条记录所引用的 ToR 设备和端口，因此只能解析 GPU 端，不能验证 `tor_sn + tor_port → port_uuid` 的结果；ToR 端应保留为 `unresolved`。设备 SN 既是来源匹配键，也是最终设备身份；未匹配对象不伪造身份。

### 暂不建立网卡实体

用户确认 `ib8` 等表示 GPU 关联网卡的端口，不是 GPU 自身的物理网络口。

```text
服务器 SN ─包含→ GPU UUID ─经关联网卡上联→ 交换机端口 UUID
                                  属性：gpu_port=ib8
```

- GPU 保留 `conn_ports` 列表；上联关系保留 `gpu_port`、`gpu_port_speed`、`server_port_speed`、`tor_port_speed`、`bond_name` 及来源信息。
- 按当前已确认的数据范围，GPU 卡 IP 的候选来源字段是 `gpu_uplink.gpu_ip`。按 `gpu_sn → server_gpu.parts_sn` 解析 GPU UUID 后，拟将非空 `gpu_ip` 合并到 GPU 的 `ip_addresses[]` 并去重；当前 8 条样例全部为空字符串，因此只能确认字段存在，尚未通过非空样例验证其值格式和归属语义。关系上仍保留原始 `gpu_ip` 以便溯源。
- `gpu_uplink.uuid` 用于关系记录身份与溯源，不是 GPU UUID。多条上联不能仅因 GPU 与交换机相同就合并。
- 网卡端口名只在所属服务器范围内识别；不为 `ib8` 虚构全局 UUID，不据此创建独立网卡/网卡端口节点。
- 以后有网卡身份及管理需求时，可细化为 GPU → 网卡/网卡端口 → 交换机端口；保留当前上联查询作为汇总表达，GPU 与交换机端口身份不变。
- 请求样例中的注释和尾逗号实际发送时须去掉；GPU 上联文档里的端口组模板说明不作为样例未提供分组字段的证据。

## 12. 最终节点、关系与属性清单

本节作为下一版 Go 领域模型、API 契约和 Nebula schema 的输入。当前 `docs/schema/current_graph.ngql` 中的地址节点及设备地址关系不再作为目标模型；设备和 GPU 地址改为内嵌属性，精确 IP 查询由 MySQL 辅助倒排索引解析资源稳定身份后再查询图。本项目开发空间允许在确认后按本节重建。

### 12.1 统一身份和同步属性

设备节点统一使用 `device_sn` 作为最终身份，`device_view.uuid` 不保存。网络设备的 `device_uuid` 只作为端口归属别名。其他 CMDB 资源节点继续使用各自已确认的稳定资源 UUID；网络端口使用 `port_uuid`，端口视图顶层 `uuid` 仅作来源记录身份。GPU 上联不是节点，使用 `gpu_uplink.uuid` 标识来源关系记录。

建议 VID 采用 `scope_id:kind_code:stable_identity`，其中设备的 `stable_identity=device_sn`，其他节点按节点清单使用稳定 UUID。现有 `FIXED_STRING(67)` 和“资源 ID 必须为 32 位十六进制”的限制不再适用；重建 schema 时应按 SN 和 UUID 的实际最大长度统一调整，暂不在本文固化长度。

所有节点统一保留：

| 属性 | 语义 |
|---|---|
| `scope_id` | 本项目租户或拓扑范围 |
| `source_id` | MySQL 中配置的 CMDB 来源 |
| 稳定身份字段 | 按节点类型保存：设备为 `device_sn`，其他节点按节点清单使用稳定 UUID |
| `inst_id`、`obj_id` | 来源实例和对象类型标识，用于查询、解析与核对；设备顶层 `uuid` 不保存 |
| `created_at` | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 本项目最后见到时间，每轮统一刷新为 `T`，用于成功轮次后的清理 |
| `source_created_at` | 来源 `create_time` 或 `created_at`；缺失时为空 |
| `source_updated_at` | 来源 `last_time`、`updated_time` 或 `updated_at`；只作业务更新时间 |
| `resolution_status` | `resolved`、`unresolved` 或 `conflict`，表示来源引用解析状态 |

所有关系统一保留 `scope_id`、`source_id`、`relation_id`、`created_at`、`synced_at`、`source_updated_at` 和 `resolution_status`。`relation_id` 优先使用来源关系 UUID；没有关系 UUID 时，由关系类型、来源字段和两端稳定身份确定性生成。设备端稳定身份为 `device_sn`。Nebula 的起点、终点和 rank 必须与 `relation_id` 保持幂等映射。

### 12.2 节点清单

| 图节点 | 来源 | 身份 | 说明 |
|---|---|---|---|
| `device` | `device_view` | `device_sn` | 统一承载服务器、网络设备及其他设备；不保存会变化的顶层 `uuid`，网络设备 `device_uuid` 仅作端口归属别名；地址作为内嵌属性 |
| `interface` | `port_view` | `port_uuid` | 网络设备端口；当前十条样例中 `port_uuid=local_interface_uuid`，顶层 `uuid` 仅保存为 `source_record_uuid`，不为服务器虚构端口；全量唯一性与长期稳定性仍以接口契约为准 |
| `gpu` | `server_gpu`，地址候选来自 `gpu_uplink.gpu_ip` | `server_gpu.uuid` | GPU 卡实例；`parts_sn` 用于来源匹配和校验，非空 `gpu_ip` 的合并规则尚待非空样例验证 |
| `data_center` | `idc` | `uuid` | 资产查询与同步范围；承载数据中心身份、位置和运营状态 |
| `pod` | `pod` | `uuid` | 设备逻辑归属节点；以 `plane` 区分管理面、计算面、存储面和带外管理面 |
| `cabinet` | `cabinet` | `uuid` | 设备位置与 A/B 路供电链路的连接节点 |
| `rpp` | `idc_RPP` | `uuid` | 列头柜；不另建“空开”节点 |
| `ups_group` | `idc_ups_group` | `uuid` | UPS 组，不等同 UPS 设备 |
| `ups` | `idc_ups` | `uuid` | UPS 设备实例 |
| `transformer` | `idc_transformer` | `uuid` | 变压器；本阶段供电上游边界 |

#### `device` 属性

- 身份与名称：`device_sn`、`inst_id`、`obj_id`、`device_name`、`host_name`、`op_asset_number`、`ad_asset_number`；`device_uuid` 仅网络设备保留为端口归属别名；不保存 `device_view.uuid`。
- 分类：`parent_type`、`parent_type_id`、`device_type`、`device_type_id`、`role`。
- 厂商与规格：`manufacturer`、`model`、`configure`。
- 状态与管理：`service_status`、`service_status_id`、`operation`、`asset`、`status_updated_at`。
- 机柜引用：`cabinet_uuid`、`idc_cabinet_id`、`cabinet`、`u_position`、`u_start`、`u_end`、`height`。
- 上层空间引用：`building_uuid`、`building_id`、`room_uuid`、`room_id`、`idc_id`、`idc`、`idc_module_id`、`logic_idc_uuid`、`logic_idc_id`。楼栋、房间、模组和逻辑 IDC 当前不生成节点。
- POD 引用：`plane`、`pod_uuid`、`pod_id`、`pod_name`、`pod_mode`、`compute_plane`、`compute_pod_id`、`compute_pod_name`、`compute_building_id/code/name`；用于生成和校验 `device → pod` 关系。
- 电力及 GPU 汇总：`power`、`rated_power`、`parts_gpu`、`pkg_gpu_count`、`pkg_gpu_manufacturer`、`pkg_gpu_model`、`gpu_is_domestic`、`gpu_performance`。GPU 汇总字段不替代 GPU 卡节点。
- 地址：`all_ips[]` 保存规范化、去重后的全部有效 IP；`device_ip_info[]` 保存 `type`、`purpose`、`ip`、`gateway`、`mask`、`mac`、`port`、`vlan_id`。带外地址由 `ilo` 类型映射为 `purpose=out_of_band`，可经辅助 IP 索引反查设备 `device_sn`。
- 上联来源：`server_tor_ports`、`server_tor_sn` 仅用于生成和校验普通服务器到 ToR 端口的 `server_uplink`，不据此生成服务器端口节点。

#### `interface` 属性

- 来源记录：`source_record_uuid`（`port_view.uuid`）、`port_id`、`local_interface_id`。
- 归属：`device_uuid`、`local_device_uuid`、`local_device_id`、`local_device_sn`；当前目标模型只为 `port_view` 返回了 `port_uuid` 的网络设备端口建立接口节点，长期稳定性待接口契约确认。
- 接口：`port_name`、`port_type`、`port_speed`、`port_operation_status`、`port_role`、`local_if_index`、`snmp_ifindex`、`local_slot`、`local_inter_ip`、`port_limit_bandwidth`。
- 对端待解析引用：`remote_interface_uuid`、`remote_interface_id`、`remote_device_uuid`、`remote_device_id`、`remote_device_sn`。
- 端口组：`group_id`、`group_uuid`、`group_type`、`group_subtype`、`port_group_id`、`port_group_uuid`、`port_group_name`、`port_group_type`、`port_group_type_name`、`port_group_role`、`port_group_physics_bandwidth`。
- 位置引用：`rack_uuid`、`room_uuid`、`local_pod_uuid`、`local_logic_idc_uuid`。

#### `gpu` 属性

- 资产：`parts_sn`、`parts_number`、`manufacturer`、`model`、`specification`。
- 位置：`index`、`slot`、`bmc_slot`、`bus_address`、`connector`、`conn_ports`。
- 规格：`VRAM`、`tdp`、`driver_ver`、`firmware_ver`、`computer_perf`、`gpu_fp`、`gpu_fp64`、`FP16_Tensor_Sparsity`、`FP4`、`INT4`、`INT8`、`TF16`、`TF32`。
- 管理：`is_domestic`、`maintenance_status`、`maintenance_end`、`device_sn`。
- 地址：拟使用 `ip_addresses[]` 汇总已匹配 GPU 的非空 `gpu_uplink.gpu_ip`，过滤空值并去重；当前 `server_gpu` 样例没有卡级 IP 字段，8 条 `gpu_uplink.gpu_ip` 也全部为空，需用非空样例确认后再固化契约。
- `server_gpu.id`、槽位和卡序号不作为 GPU 实例身份。

#### `data_center` 属性

- 身份与名称：`uuid`、`inst_id`、`obj_id`、`code`、`cn_name`、`alias`；UUID 为最终身份，数字 ID 仅用于引用解析。
- 位置：`address`、`location`、`city_id`、`zone_id`、`geographic_location_id`、`latitude`、`longitude`；当前样例经纬度均为 0，来源文档未定义其含义，本项目暂按未知坐标处理而不是有效的 `(0,0)`。
- 状态：`status`、`is_delete`、`idc_ops_status`、`idc_service_level`、`open_at`、`close_at`。
- 统计快照：`cabinet_num`、`cabinet_power_num`、`device_num`、`server_num`、`network_device_num`、`external_num`、`other_num`；可用于展示和核对，不能替代图中实际关系计数。
- 联系人、电话、邮箱及支持人员列表不进入图模型；若有运维查询需求，保留在业务存储或受控接口中。

#### `pod` 属性

- 身份与名称：`uuid`、`inst_id`、`obj_id`、`basic_code`、`name`、`full_name`；UUID 为最终身份，`inst_id` 用于设备引用解析。
- 平面与网络：`plane`、`mode`、`rdma`；`plane` 保留来源数值，当前已验证 1=管理面、2=计算面，其他枚举按接口定义保留。
- 归属引用：`phy_building_id`、`idc_logic_id`；楼宇仅用于交叉校验，逻辑 IDC 关系尚未确认。
- 状态与时间：`is_delete`、`is_sync`、`source_created_at`、`source_updated_at`。
- 同一 `pod` 类型承载所有平面，不因管理面或计算面拆成不同节点类型；`compute_plane` 本身不是节点。

#### `cabinet` 属性

- 编码与位置：`code`、`dc_colo_rack`、`isp_rack_code`、`rack`、`rack_column_code`、`rack_row`、`idc_rows_code`。
- 空间引用：`idc_id`、`phy_idc_id`、`phy_building_id`、`phy_room_id`、`idc_module_id`、`idc_building_structure_id`、`pod_id`、`pod_ids`。
- 容量：`u_num`、`spare_u_num`、`used_u_num`、`device_num`、`server_num`、`network_device_num`、`gpu_device_num`。
- 状态与类型：`is_delete`、`status`、`ops_status`、`usage_type`、`power_status`、`power_state`、`power_type`。
- 电力：`rated_current`、`isp_rated_current`、`maximum_current`、`allow_use_current`、`tec_max_current`、`pdu_num`、`outlets_10a`、`outlets_16a`。
- TOR 规划：`rack_tor_fix`、`rack_tor_inter_speed`、`rack_tor_type`，只作属性。
- 供电引用：`row_switch_id_A`、`row_switch_id_B` 均解析到 `idc_RPP.inst_id`，分别生成 `power_path=A/B` 的 `power_upstream` 关系；两路使用相同逻辑。

#### 供电节点属性

| 节点 | 类型专属属性 |
|---|---|
| `rpp` | `code`、`idc`、`building`、`idc_room`、`module`、`row_in_num`、`row_power_type`、`row_rated_current`、`row_rated_voltage`、`ups_group`、`transformer_group_a` |
| `ups_group` | `code`、`idc`、`building`、`module`、`ups_group_id_tag`、`ups_group_num`、`ups_external_bypass`、`ups_only_IT`、`ups_only_jd`、`ups_proportion_jd`、`transformer_id_up` |
| `ups` | `code`、`idc`、`building`、`module`、`ups_group`、`transformer_id_up`、`ups_brand`、`ups_type`、`ups_capacity`、`ups_group_id_tag`、`ups_product_date`、`ups_exprie_date`、`ups_capacitor_change_date`、`battery_brand`、`battery_type`、`battery_group_num`、`battery_num`、`battery_voltage`、`battery_resistance`、`battery_product_time` |
| `transformer` | `code`、`idc`、`building`、`module`、`standby_transformer`、`transformer_brand`、`transformer_type`、`transformer_capacity`、`transformer_id_tag`、`transformer_insulation_level`、`transformer_only_IT`、`transformer_only_jd`、`transformer_over_temperature_alarm`、`transformer_over_temperature_cut`、`transformer_product_time`、`transformer_expire_time`、`LVP_busbar`、`LVP_generator_num`、`LVP_logic_state` |

单位或枚举未明确的属性先按来源类型或字符串原值保存，不提前转换。未列入上述清单的来源字段不自动进入 Nebula；后续若成为查询、关系解析或审计所需字段，再通过显式 schema 变更加入。

### 12.3 关系清单

| 图关系 | 方向 | 来源与关系身份 | 关系属性 |
|---|---|---|---|
| `owns_interface` | `device → interface` | `local_device_uuid → device_view.device_uuid`，并以 `local_device_sn/device_sn`、`local_device_id/inst_id` 交叉校验，解析为设备 `device_sn` 后关联 `port_uuid` | 按设备 SN 与端口 UUID 确定性生成；当前不为服务器生成该关系 |
| `server_uplink` | `device → interface` | 服务器 `server_tor_ports[].sn` 直接解析 ToR `device_sn`，再以 `ports[]` 在该设备范围内解析 `port_uuid`；无来源关系 UUID，按服务器 SN、ToR 端口 UUID 和规范化来源字段集合确定性生成 | `tor_sn`、`tor_port`、`tor_ip`、`ip_type`、`as_number`；当前样例端点未闭环，只能得到 `unresolved` 引用 |
| `links_to` | `interface → interface` | 网络端口的本端 `port_uuid` 与 `remote_interface_uuid`；按端点对确定唯一逻辑关系，合并可能的两端重复上报 | `local_source_record_uuid`、`remote_source_record_uuid`；去重是模型规则，当前十条样例未包含可成对验证的双端记录；不把 CMDB 连接直接解释为实时可达 |
| `contains_gpu` | `device → gpu` | `server_gpu.device_sn = device_view.device_sn`，设备端使用 `device_sn`，GPU 端使用 `server_gpu.uuid` | `device_sn`、`parts_sn` |
| `gpu_uplink` | `gpu → interface` | `gpu_uplink.uuid`；先由 `gpu_sn` 解析 GPU，再由 `tor_sn` 和 `tor_port` 解析网络端口 | `gpu_port`、`gpu_port_speed`、`gpu_ip`、`gpu_slot`、`server_port_speed`、`bond_name`、`tor_port`、`tor_port_speed`、`tor_role`、`source`；当前样例只闭环 GPU 端，ToR 端为 `unresolved`；非空 `gpu_ip` 汇总规则待样例验证 |
| `member_of` | `device → pod` | 管理面：`pod_uuid → pod.uuid`；计算面：逐项解析 `compute_plane[].pod_id → pod.inst_id`；解析后按设备 SN、POD UUID 和关系类型去重 | `plane`、`source_fields`、`building_id`；`pod_id/name`、`compute_pod_id/name` 仅用于校验，冲突时标记 `conflict` |
| `located_in` | `device → cabinet` | `device.cabinet_uuid → cabinet.uuid`，按设备 SN 与机柜 UUID 建立关系 | `link_kind=device_cabinet`、`u_position`、`u_start`、`u_end` |
| `located_in` | `cabinet → data_center` | `cabinet.idc_id/phy_idc_id → data_center.inst_id`，解析后使用两端 UUID | `link_kind=cabinet_data_center`、`source_field`；双字段不一致时标记 `conflict` |
| `power_upstream` | `cabinet → rpp` | `row_switch_id_A/B → rpp.inst_id`，按来源字段和两端 UUID 生成 | `link_kind=cabinet_rpp`、`power_path=A/B`、`source_field` |
| `power_upstream` | `rpp → ups_group` | `rpp.ups_group → ups_group.inst_id` | `link_kind=rpp_ups_group`、`source_field` |
| `member_of` | `ups → ups_group` | `ups.ups_group → ups_group.inst_id` | `ups_group_id_tag` |
| `power_upstream` | `ups_group → transformer` | `ups_group.transformer_id_up → transformer.inst_id` | `link_kind=ups_group_transformer`、`source_field` |
| `has_standby` | `transformer → transformer` | 当前变压器的 `standby_transformer` 解析到备用变压器 `inst_id` | `source_field=standby_transformer`；当前样例目标 1593 未提供，只保留 `unresolved` 引用，默认不计入当前供电路径 |

端口物理连接只保存一个逻辑关系，查询时按双向遍历处理；不能因为同一设备间有多条端口连接就合并。供电统一沿“下游对象指向上游对象”保存，影响范围查询反向遍历。来源引用冲突时标记 `conflict`，不静默选择一条关系。

### 12.4 明确不建立的节点和关系

- 不拆分“服务器节点”和“网络设备节点”；通过 `parent_type`、`device_type` 和 `role` 筛选统一 `device`。
- 不建立地址节点及 `owns_address`、`has_address` 关系。设备保存 `all_ips[]` 和结构化 `device_ip_info[]`，GPU 保存 `ip_addresses[]`；精确 IP 反查使用 MySQL 辅助索引，不把 IP 当作图身份。
- 不建立服务器端口节点。普通服务器使用 `server_tor_ports` 直接生成 `device → ToR interface` 的 `server_uplink`；网络设备端口仍由 `port_view` 建立。
- 不建立网卡及网卡端口节点；`ib8` 等保存在 `gpu_uplink.gpu_port`，GPU 卡地址只从 `gpu_uplink.gpu_ip` 汇总。
- 不建立端口组节点；组 UUID、成员 UUID 和三级分类保存在端口属性中。
- 缺少明确 LAG 成员资料前，不生成 `aggregates` 关系；端口名包含 `AggregatePort` 不能作为成员证据。
- 建立轻量 `data_center` 和统一 `pod` 节点；不建立楼栋、房间、模组和逻辑 IDC 节点，现阶段保留这些对象的引用属性。
- 不建立 `compute_plane` 节点；该数组只作为设备到计算面 POD 的关系解析和一致性校验依据。
- 不根据 `row_switch_id_A/B` 建立独立空开节点，也不根据 `transformer_group_a` 建立“变压器组”节点。
- 不建立 `rpp → transformer` 图关系；`rpp.transformer_group_a` 仅作为节点属性和同步校验字段，不参与供电路径遍历。
- 不建立 `ups → transformer` 图关系；`ups.transformer_id_up` 仅用于与所属 UPS 组的上游变压器做一致性校验。
- 不建立 GPU 上联记录节点、端口视图记录节点或来源同步记录节点；这些使用关系属性或 MySQL 同步控制数据表达。
- 不展开 PDU、馈线、市电和发电机关系；变压器是本阶段供电上游边界。

### 12.5 时间清理对模型的要求

1. 上述每类节点和关系都必须包含 `created_at` 与 `synced_at`，不能只给节点加时间。
2. 每轮开始记录唯一时间 `T`；本轮见到或重新生成的记录全部刷新为 `synced_at=T`。
3. 默认全量轮次覆盖本文十一类 API；只有该轮配置范围内所有必需接口的分页采集、身份解析、关系生成和图写入全部成功后，才能清理当前来源、scope、对象类型和同步范围内 `synced_at<T` 的数据。若未来增加独立的类型级全量任务，必须使用独立同步范围，只能清理其明确完整覆盖的类型。
4. 清理先删旧关系，再删不再被有效关系引用的旧节点。任一步失败都不报告本轮成功。
5. CMDB `last_time`、`updated_at` 等只写入 `source_updated_at`，不参与“本轮是否见到”的判断。
6. 同一来源和重叠范围的轮次串行执行；可选 `sync_run_id` 只用于 MySQL 审计，不是图数据清理前提。

## 13. 后续执行顺序

1. 以本节清单为目标，重建本项目独立 Nebula schema：调整 VID，删除地址节点及其关系，不建立服务器端口节点，加入 `server_uplink`、统一 `pod` 和 `device → pod` 的 `member_of`，删除没有来源支撑的 `aggregates`。
2. 同步调整 Go 领域模型、图 repository、API DTO 和查询过滤：设备增加 `all_ips[]`、结构化 `device_ip_info[]`，GPU 增加 `ip_addresses[]`，MySQL 增加精确 IP 反查辅助索引，并保留当前态读取的 scope/epoch 保护。
3. 实现 11 类 CMDB adapter、统一分页器、5 并发限制、`inst_id → uuid` 解析及 SN 辅助匹配。
4. 实现 worker 全量轮次：统一时间 `T`、幂等写入、全成功后按 `synced_at<T` 清理；失败不清理。
5. 增加 fake repository 单元测试、Nebula 查询测试和独立开发空间只读/写入验证，再接入定时执行。

`topology_grid` 始终只读。本项目开发空间允许重建，但实际执行 DDL 或删除现有空间前仍需确认目标和影响范围。
