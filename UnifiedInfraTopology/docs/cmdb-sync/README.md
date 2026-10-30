# CMDB 同步现状与验收

> 本目录仅供人工阅读，不参与运行或配置加载。
> 本文记录 2026-10-30 阶段交付状态；模型语义仍以 `docs/model/` 为准。

## 已实现范围

| 实体或关系 | 本地 IDC1030 重建后数量 | 来源或规则 |
| --- | ---: | --- |
| device | 4,698 | `device_view`，服务器 2,518、网络设备 2,180 |
| data_center | 1 | `idc`，条件 `inst_id: 1030` |
| cabinet | 2,671 | `cabinet`，条件 `idc_id: 1030` |
| transformer | 71 | `idc_transformer`，条件 `idc: 1030` |
| interface | 174,482 | 网络设备逐 SN 采集物理端口，身份为 `port_uuid` |
| owns_interface | 174,482 | 已存在的网络设备 → 物理接口 |
| server_uplink | 2,710 | 服务器 → 物理接口，可沿归属边查到 ToR |

这些数量是本地测试验收快照，不是生产规模承诺。当前证据见
[`reports/local-reset-resync-2026-10-30.json`](../../reports/local-reset-resync-2026-10-30.json)。
本地已按用户授权清空旧资产数据及同步记录后重建；来源、用户、配置与 Schema 保留。
旧数据库任务 ID 已失效，不得用历史报告的任务 ID 验收当前数据。

## 采集范围与身份

- 开发阶段保留 IDC1030 条件，不自动采集其他机房。
- 物理接口先发现 IDC 内分类为 54 的网络设备，再逐台以 `sn` 查询 `port_view`；当前限定 `port_type: physics`。
- 上联以分类为 52 的服务器 `device_view.server_tor_ports[].sn + ports[]` 为事实来源，按 ToR SN 与端口名解析 `port_uuid`。
- 上联目标 ToR 和端口按 SN 查询不附加 IDC 条件；服务器发现仍限定 IDC1030。按 SN 查询跨机房来源，不等于自动在图中创建跨机房端点。
- 设备身份是 `device_sn`，接口身份是 `port_uuid`，其他已实现实体身份是 `uuid`。来源数值引用不能代替稳定身份。
- 按设备分区分页允许不同设备的端口 `inst_id` 重置，但设备内必须严格递增；检查点保存真实分区值，不合成来源 ID。

## 任务与发布保护

- 普通任务 API 接收 `source_id` 和 `mode: full`；`full` 仍受来源配置条件限制，不意味着全部 IDC。
- Worker 维护任务状态、租约、取消和检查点，并进行两遍来源数量、身份与归属校验。
- 过滤采集跳过整源点边清理。历史关系在来源删除或改归属后可能保留，不能视为已经完成旧边收敛。
- `owns_interface` 写入前校验同来源设备已存在，接口顶点成功后才写边。
- `server_uplink` 要求服务器、接口和 ToR 均已存在，分类正确，物理接口唯一归属于来源指定的 ToR。歧义或缺失记录安全跳过并计数，不创建占位设备或服务器接口。
- 图写入不提供整轮回滚；后续检查失败可能留下部分写入。读取端点不能原子防止外部删点。
- MySQL 存控制面，NebulaGraph 存当前资产点边；本阶段未自动执行 Schema 迁移。

## 配置与运行

配置示例见 [`config/local.example.yml`](../../config/local.example.yml)。实际凭据只保存在被忽略的本地配置中。
`sources.config_ref` 对应 `inventory.cmdb.sources.<config_ref>`。

定向同步并自动验收：

```sh
go run ./cmd/worker -conf config/local.yml -sync-filtered \
  -source-id 00000000000000000000000000001030 -entity-type device
```

`-entity-type` 支持 `device`、`data_center`、`cabinet`、`interface`、`transformer`。
重建顺序为设备 → 数据中心/机柜/变压器 → 接口及归属 → 服务器上联。

```sh
go run ./cmd/worker -conf config/local.yml -sync-server-uplink \
  -source-id 00000000000000000000000000001030
```

只读验收：

```sh
go run ./cmd/worker -conf config/local.yml -verify \
  -source-id 00000000000000000000000000001030 \
  -entity-type interface -run-id <本轮任务ID>

go run ./cmd/worker -conf config/local.yml -verify-server-uplink \
  -source-id 00000000000000000000000000001030 -run-id <本轮任务ID>
```

不提供 `-run-id` 时仅进行预检，不宣称同步成功。
真实写入、数据清理和报告导出均需明确授权；日常交付优先使用同一 Plannotator Inbox 线程。

## 验收证据与已知问题

- 已执行真实 CMDB → MySQL/NebulaGraph 同步，再通过独立进程查询数量、任务状态、租约和检查点。
- 接口验收检查当前快照节点身份、时间和属性类型；归属边验收对照有效端点路径。
- 上联验收逐边核对身份、rank、端点分类和唯一归属；新来源抽样验证服务器 → 物理端口 ← ToR 路径。
- 重建后图端点缺失候选从 760 条降为 0，剩余 6 条已由用户确认属于 CMDB 数据问题：
  - 2 条：ToR `3G2610019347` 的来源端口名 `GE0/0/5～6` 与 `GigabitEthernet0/0/5～6` 不一致，属于端口名称清洗问题。
  - 4 条：ToR `102618636126` 的 `25GE1/0/5～8` 无 `port_view` 库存，属于上游采集问题。
- 对上述 6 条仅记录，不添加本地别名补丁、不伪造端口或关系。
- 专项/竞态测试、静态检查和入口构建已执行通过。全量测试结果应以当前运行输出为准，不能沿用历史状态声明。

## 尚未实现或验证

POD、GPU、RPP、UPS 组和 UPS 的采集管线，`links_to` 及其他空间、电力、GPU 关系尚未接入。
虚拟接口、生产环境、其他 IDC、大规模性能，以及全部属性与上游逐字段对照尚未验收。
双遍校验不等同于 CMDB 的原子快照，来源并发变化仍有边界。
