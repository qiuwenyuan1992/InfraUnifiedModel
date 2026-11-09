# CMDB 同步现状与验收

> 本目录仅供人工阅读，不参与运行或配置加载。
> 最新验收为2026-11-03 IDC238清空重建；模型语义仍以 `docs/model/` 为准。
>
> 用户批准只清空IDC238同步图数据：41827个节点及关联边已删除，10个Tag和4种Edge的来源计数全部归零，Schema/索引/MySQL历史均保留。随后15阶段完整同步及独立进程复核全部PASS，重建数量与清空前相同。最新快照 `2026-11-03T05:51:16.722422Z`；接口35234（含聚合口）、POD成员1445、GPU1289/卡上联455、服务器上联1661、空间三种新组合5636。
>
> 开发过程的历史报告已按用户要求清理。下文数量、快照与run_id仅记录当时验收结果，不代表当前图状态；需要新证据时重新执行受控同步或只读验收。

## 阶段交付范围

本阶段提交整合以下成果：

- CMDB电力、POD、GPU和空间关系采集，GPU使用真实 `parts_sn` 身份，端口包含聚合口。
- 15阶段完整同步、共享快照时间及独立验收；附开发图库清空重建证据。
- 后台同步管理API、持久化阶段进度、取消、诊断和独立队列worker，更新Wire、Swagger及控制面迁移。
- 保留显式授权的live测试，旧开发报告已清理；默认测试不会采集CMDB或修改图库。本地凭据配置不进入提交。

上述为阶段提交时的范围。后续API已做真实HTTP/MySQL采集联调，另建立独立 `UnifiedInfraWeb` 管理前端；实时联调说明见接口文档。

仍未交付：每日定时调度、整轮成功后的旧快照清理、全IDC采集范围扩展、图谱/故障查询与服务常驻部署。

## 后台API第一期

已增加 `/v1/inventory/sync-rounds` 的异步创建、分页列表、详情/15阶段进度、取消及整轮诊断接口；单阶段任务也可查询诊断。默认JWT加 `sync:read/write` 控制权限；开发环境可通过 `inventory.auth.enabled: false` 关闭（固定操作身份），HTTP只入队，worker以 `-process-rounds` 执行。

本地 `0003_sync_rounds.sql` 控制面迁移已审批应用并只读验证；部署其他数据库必须先执行迁移。保留现有Schema、资产及来源，不做来源增删改。HTTP服务及队列worker尚未持续启动，联调时需按文档启动并配置实际用户权限。`UnifiedInfraWeb` 第一版不包含登录和权限页面，直接接同步管理API；开发环境关闭后端鉴权，部署开放网络前必须开启。每日调度、旧数据清理、全IDC范围扩展及故障浏览仍未实施。拓扑浏览已调整为数据中心→机柜→设备及POD→成员设备下钻，自动使用唯一CMDB来源，设备视图有界读取真实供电/GPU/网络路径，并保留手动展开与属性查看；说明见 [`topology-browser.md`](topology-browser.md)。管理接口与启动步骤见 [`sync-management-api.md`](sync-management-api.md)。

## IDC238独立阶段的历史验收

用户授权清空 IDC1030 测试资产后，本地来源切换为 `idc238`，来源 ID 为 `00000000000000000000000000000238`；旧来源已停用。
设备 2,408、数据中心 1、机柜 2,573、物理接口与归属各 23,878、服务器上联 1,661、互联 3,026，已独立验收。
POD实体新增12个，仅发布IDC238设备与机柜引用匹配的UUID节点；读取1,800条POD目录后筛选，未发布全目录。设备→POD的 `spatial_relation/member_of` 已发布1,436条，18条计算面引用与目标plane不匹配跳过；设备节点未重写。POD实体证据见 （历史开发报告已清理）。
电力身份已按用户确认纠正：transformer、ups_group、ups、rpp 以正整数 `inst_id` 为权威身份，不要求 UUID。此前阻断是本项目契约错误，不是 CMDB 缺字段。
限定四类电力 Tag 的 UUID 列和身份索引已迁移，现库 `inst_id INT NULL` 由应用强制非空正整数；新库基线 `INT NOT NULL`，不伪造默认身份。
重跑已写入变压器24、UPS组37、UPS67、RPP182及电力关系5,438条（供电上游5,349、UPS成员67、备用22），机柜A/B→RPP→UPS组→变压器路径已独立验收。
机柜来源12个零引用未发布；用户确认允许互为备用后，11组二节点互备的22条有向边已发布。用户进一步确认多台备用循环也允许，发布与验收不再因成环拒绝；仍拒绝自引用及无效端点，备用不进入默认主供电链。110条设备级变压器引用与组上游不一致仅记录诊断，保留明确的主链事实。
互备、电力及先前重建的开发报告已清理；上述结论仅为当时验收摘要。
以下 IDC1030 数量和示例保留为历史阶段证据，**不表示当前数据库状态**；运行当前数据请改用 IDC238 来源 ID 与实际本地配置。

## 已实现范围（历史 IDC1030 验收）

| 实体或关系 | 本地 IDC1030 重建后数量 | 来源或规则 |
| --- | ---: | --- |
| device | 4,698 | `device_view`，服务器 2,518、网络设备 2,180 |
| data_center | 1 | `idc`，条件 `inst_id: 1030` |
| cabinet | 2,671 | `cabinet`，条件 `idc_id: 1030` |
| transformer | 71 | `idc_transformer`，条件 `idc: 1030` |
| ups_group | 115 | `idc_ups_group`，条件 `idc: 1030` |
| ups | 303 | `idc_ups`，条件 `idc: 1030` |
| rpp | 417 | `idc_RPP`，条件 `idc: 1030` |
| power_relation | 837 | 上游532、UPS成员303、备用2；机柜A/B来源全0，未生成机柜供电边 |
| interface | 174,482 | 网络设备逐 SN 采集物理端口，身份为 `port_uuid` |
| owns_interface | 174,482 | 已存在的网络设备 → 物理接口 |
| server_uplink | 2,710 | 服务器 → 物理接口，可沿归属边查到 ToR |
| links_to | 36,404 | 两网络设备物理接口互联，双端上报归一为一条边 |

这些数量是本地测试验收快照，不是生产规模承诺。相关重建、互联、UPS组及电力开发报告已清理。
本地已按用户授权清空旧资产数据及同步记录后重建；来源、用户、配置与 Schema 保留。
旧数据库任务 ID 已失效，不得用历史报告的任务 ID 验收当前数据。

## 采集范围与身份

- 开发阶段保留 IDC1030 条件，不自动采集其他机房。
- 接口先发现 IDC 内分类为 54 的网络设备，再逐台以 `sn` 查询 `port_view`；默认不附加 `port_type` 条件，包含物理口与聚合口，按来源保留实际类型。ToR上联解析及图验收同样不限定物理口，保留UUID、名称、归属唯一性检查。`links_to` 仍遵循物理连接契约，不扩大为聚合口成员关系。
- 上联以分类为 52 的服务器 `device_view.server_tor_ports[].sn + ports[]` 为事实来源，按 ToR SN 与端口名解析 `port_uuid`。
- 上联目标 ToR 和端口按 SN 查询不附加 IDC 条件；服务器发现仍限定 IDC1030。按 SN 查询跨机房来源，不等于自动在图中创建跨机房端点。
- `links_to` 使用源物理口 `port_uuid` 与对端 `remote_interface_uuid`，按对端 SN 查询权威网络设备及物理口记录，双端上报按模型规范身份去重。缺少对端、不唯一或图端点不满足条件时不造边。
- 设备身份是 `device_sn`，接口身份是 `port_uuid`；四类电力实体身份为正整数 `inst_id` 的规范十进制字符串，图库绑定整数；数据中心、机柜等身份仍为 `uuid`。引用按目标类型和 `inst_id` 唯一解析，不伪造 UUID。
- 按设备分区分页允许不同设备的端口 `inst_id` 重置，但设备内必须严格递增；检查点保存真实分区值，不合成来源 ID。

## 任务与发布保护

- 普通任务 API 接收 `source_id` 和 `mode: full`；`full` 仍受来源配置条件限制，不意味着全部 IDC。
- Worker 维护任务状态、租约、取消和检查点。当前用户批准的流程：一次完整采集（含分页/目录查询），身份/全局冲突/端点校验→写图→独立查图库验收。写图后不再次读取 CMDB，不以第二遍来源差异产生 `source_changed`；独立只读调试测试可比较两次来源调用，但不属于生产同步流程。
- 过滤采集跳过整源点边清理。历史关系在来源删除或改归属后可能保留，不能视为已经完成旧边收敛。
- `owns_interface` 写入前校验同来源设备已存在，接口顶点成功后才写边。
- `server_uplink` 要求服务器、接口和 ToR 均已存在，分类正确，接口（含聚合口）唯一归属于来源指定的 ToR。歧义或缺失记录安全跳过并计数，不创建占位设备或服务器接口。
- `links_to` 两端必须为同来源、分类 54 网络设备唯一拥有的物理接口；独立发布边，不重复写接口节点。来源读取最多四页并行，端点检查和写边最多八个候选并行，在一次完整采集中按固定来源顺序归一化并校验，不进行写后来源复读。
- 图写入不提供整轮回滚；后续检查失败可能留下部分写入。读取端点不能原子防止外部删点。
- MySQL 存控制面，NebulaGraph 存当前资产点边；本阶段未自动执行 Schema 迁移。

## 新空间任务与统一轮次

新增入口已完成IDC238真实同步与独立验收：

```bash
go run ./cmd/worker -conf config/local.yml -sync-spatial -source-id <source_id>
go run ./cmd/worker -conf config/local.yml -verify-spatial -source-id <source_id> -run-id <spatial_run_id>
go run ./cmd/worker -conf config/local.yml -sync-round -source-id <source_id>
```

空间任务仅发布设备→机柜、机柜→IDC的 `located_in` 与机柜→POD的 `tagged_with`；设备→POD继续使用独立成员任务。没有机房层级或推导关系，不重写节点，不自动清理旧边。

统一轮次依次执行九种常规实体（GPU由独立任务采集）、电力、POD成员、新空间、GPU、服务器上联及物理 `links_to`。各阶段独立配置、run_id与检查点，共享同一UTC微秒级 `snapshot_at`；租约使用实时钟，失败停止后续阶段并保留既有写入。共享时间不是跨API的原子来源快照。

需要独立复核统一轮次里的单个任务时，传入轮次报告中的时间：

```bash
go run ./cmd/worker -conf config/local.yml -verify-spatial -source-id <source_id> -run-id <spatial_run_id> -snapshot-at <round_snapshot_RFC3339>
```

`-snapshot-at` 只用于已完成任务的只读验收，不能用于写入模式。没有该参数时保留独立任务的原检查点时间验证规则。报告明确区分成功、失败、未执行阶段；没有整体回滚承诺。

## 配置与运行

配置示例见 [`config/local.example.yml`](../../config/local.example.yml)。实际凭据只保存在被忽略的本地配置中。
`sources.config_ref` 对应 `inventory.cmdb.sources.<config_ref>`。

定向同步并自动验收：

```sh
go run ./cmd/worker -conf config/local.yml -sync-filtered \
  -source-id 00000000000000000000000000001030 -entity-type device
```

`-entity-type` 支持 `device`、`data_center`、`cabinet`、`interface`、`transformer`、`ups_group`、`ups`、`rpp`、`pod`。
POD使用设备和机柜相同的正数IDC条件发现引用，读取目录筛选UUID，再按`inst_id`定向查询，目录与定向查询均属于一次完整采集，不是写后复读；不支持直接POD IDC条件，引用范围不完整时禁止整源清理。
电力节点完成后可用 `-sync-power -source-id <来源ID>` 发布全部已建模电力关系；独立验收使用 `-verify-power -source-id <来源ID> -run-id <本轮任务ID>`。
重建顺序为设备 → 数据中心/机柜/变压器 → 接口及归属 → 服务器上联。

```sh
go run ./cmd/worker -conf config/local.yml -sync-server-uplink \
  -source-id 00000000000000000000000000001030

go run ./cmd/worker -conf config/local.yml -sync-links-to \
  -source-id 00000000000000000000000000001030
```

只读验收：

```sh
go run ./cmd/worker -conf config/local.yml -verify \
  -source-id 00000000000000000000000000001030 \
  -entity-type interface -run-id <本轮任务ID>

go run ./cmd/worker -conf config/local.yml -verify-server-uplink \
  -source-id 00000000000000000000000000001030 -run-id <本轮任务ID>

go run ./cmd/worker -conf config/local.yml -verify-links-to \
  -source-id 00000000000000000000000000001030 -run-id <本轮任务ID>
```

不提供 `-run-id` 时仅进行预检，不宣称同步成功。
真实写入、数据清理和报告导出均需明确授权；日常交付优先使用同一 Plannotator Inbox 线程。

## 验收证据与已知问题

- 已执行真实 CMDB → MySQL/NebulaGraph 同步，再通过独立进程查询数量、任务状态、租约和检查点。
- 接口验收检查当前快照节点身份、时间和属性类型；归属边验收对照有效端点路径。
- 放开聚合口后已重跑IDC238服务器上联：任务 `9cd63895a3fe0a22eec485620e0c772d`，快照 `2026-11-03T02:38:05.031273Z`；采集1,956台服务器，发布1,661条server_uplink，跳过0、缺端点0。独立进程复核1,661条有效归属路径、检查点完成及租约释放。数量与旧快照相同，不据此推断任意单台GPU服务器一定有来源上联。
- 上联验收逐边核对身份、rank、端点分类和唯一归属；新来源抽样验证服务器 → 物理端口 ← ToR 路径。
- 重建后图端点缺失候选从 760 条降为 0，剩余 6 条已由用户确认属于 CMDB 数据问题：
  - 2 条：ToR `3G2610019347` 的来源端口名 `GE0/0/5～6` 与 `GigabitEthernet0/0/5～6` 不一致，属于端口名称清洗问题。
  - 4 条：ToR `102618636126` 的 `25GE1/0/5～8` 无 `port_view` 库存，属于上游采集问题。
- 对上述 6 条仅记录，不添加本地别名补丁、不伪造端口或关系。
- 专项/竞态测试、静态检查和入口构建已执行通过。全量测试结果应以当前运行输出为准，不能沿用历史状态声明。

## 当前轮次验收

统一入口 `-sync-round` 已跑完15个阶段；所有检查点、节点及边使用 `2026-11-03T03:00:24.269220Z`，各阶段StartedAt与租约仍为实时钟。另一个只读进程复核全部15个run_id及图快照，通过。完整证据见 （历史开发报告已清理）。

|关系|本轮验收数量|
|---|---|
|设备→机柜 located_in|2407|
|机柜→IDC located_in|2573|
|机柜→POD tagged_with|656|
|设备→POD member_of|1445|
|电力关系|5438|
|GPU contains_gpu / gpu_uplink|1289 / 455|
|服务器 server_uplink|1661|
|物理 links_to|3026|

实体本轮数量：IDC1、机柜2573、设备2408、POD12、接口35234、变压器24、UPS组37、UPS67、RPP182、GPU1289。空间引用1条空机柜与1993条空POD数组分别诊断跳过；不意味着必须给每个机柜关联POD。设备POD成员由1436变为1445，属于本轮来源结果，不能沿用历史数量。

POD成员及GPU实体/包含/上联的独立任务入口保留（`-sync-pod-membership`、`-sync-gpu`），对应只读入口为 `-verify-pod-membership`、`-verify-gpu`。
GPU已按用户确认改用 `source_id + gpu + parts_sn` 唯一身份；缺失/空白SN跳过，不要求卡UUID，不回退inst_id。2026-11-06 查询契约更正：先按服务器 `condition.device_sn` 完整分页采集卡并识别有效/冲突SN，再对有有效卡的服务器按 `gpu_uplink.condition.device_sn` 完整分页查询上联，不逐卡请求。返回的 `gpu_sn` 仅用于解析同一服务器所属的有效 `parts_sn`；缺失、未知SN或归属/属性冲突的记录跳过，没有有效卡的服务器不请求上联。下述数量和验收仍为历史记录，本次文档更正不代表重新执行了live验证。
以下为此前独立阶段的历史证据；其中双遍来源与最终复读仅记录旧执行流程，已被上述一次完整采集契约取代，不是当前生产验收要求。最新run_id和检查点以完整轮次报告为准。
IDC238真实GPU同步已通过验收：1,289个GPU节点、1,289条contains_gpu、455条gpu_uplink；任务 `7d27980f8895a555f9e38619a475c6b0` 检查点完成、租约释放。3,287条卡记录中1,992条无SN跳过，3个冲突SN的6条记录按用户确认全部排除，既不择一也不生成上联。
独立GPU Schema迁移 `0004_gpu_parts_sn_identity.ngql` 已审批执行，执行前全图GPU节点及相关边均为0；现身份字段与索引均为parts_sn。端口已去掉默认类型限制，35,234个接口及归属边同步成功（任务 `c1dc8ca0984e754357ebfb4912804fb3`），包含virtual聚合口。原288条Bridge-Aggregation上联全部解析并发布，当前采集的455条上联均无缺失端点；455条VRAM差异仅诊断。首个SN任务完成写入但最终复读失败，未回滚；只读双遍来源一致后幂等重跑成功。证据见 （历史开发报告已清理）。
## 剩余边界

当前模型的四类空间关系均已接入并在IDC238验收，没有机房或POD→IDC推导关系。本轮未自动清理旧边，设备换柜或GPU移机后的旧关系可能保留，不能宣称旧边已收敛。物理links_to有60条候选图端点不符、212条非物理对端诊断跳过，不扩大到跨机房占位端点；POD仍有18条plane冲突，电力仍有12条无效引用跳过。
GPU上联来源UUID未持久化，图独立验收只能检查路径、属性和存储rank，完整来源身份绑定仍需额外核对。
电力关系已覆盖机柜A/B→RPP、RPP→UPS组、UPS成员、UPS组→变压器和备用变压器；备用允许多台配置循环，仅拒绝自引用及无效端点，不进入默认主供电链。
IDC238机柜供电链已验收；历史IDC1030机柜引用全0的限制不代表当前库。设备级变压器引用一致性差异仅诊断，不生成UPS或RPP到变压器的跨层捷径。
IDC238虚拟聚合接口及GPU聚合上联已验收；生产环境、其他 IDC、大规模性能，以及全部属性与上游逐字段对照尚未验收。
一次完整采集不等同于 CMDB 的原子快照，来源并发变化仍有边界；分页完整性、身份/全局冲突/端点校验及独立查图库验收仍必须保留。
