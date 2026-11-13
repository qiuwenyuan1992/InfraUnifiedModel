# 同步管理API

第一期提供完整同步任务的异步创建、分页列表、阶段进度、取消及诊断查询；不做来源增删改、Web界面或图谱/故障查询。API只持久化任务，不在HTTP请求中调用CMDB或启动后台goroutine；独立worker执行固定15阶段。

本期保留当前IDC238配置范围，不扩大为全部数据中心，不启动每日定时调度，不自动清理旧图数据，也不承诺全轮回滚。已确认的后续策略是整轮验收成功后可删除上轮数据，即使本轮有来源缺失或异常跳过；失败则不清理。此清理策略尚未实现。

## 启动顺序

先通过专用迁移入口执行控制面增量迁移，增加整轮和阶段表，不执行NebulaGraph DDL。然后分别启动HTTP服务与队列worker：

```bash
go run ./cmd/migration -conf config/local.yml
go run ./cmd/server -conf config/local.yml
go run ./cmd/worker -conf config/local.yml -process-rounds
```

仅执行一轮排队任务时使用 `-process-rounds -once`。旧默认worker继续处理单阶段 `sync-runs`，不会抢领整轮子任务。`-sync-round` 保留为独立CLI入口，同来源存在API整轮任务时拒绝并发执行。

生产部署必须独立审批数据库迁移并配置下方权限；不要用HTTP请求代替持续运行的worker。

## 鉴权与配置

同步管理接口缺省要求JWT。`inventory.auth.enabled` 仅显式布尔值 `false` 会关闭鉴权；缺失、true或错误类型均保持开启。开启时授权用户ID及权限区分大小写，读取要求 `sync:read`，创建及取消同时要求 `sync:read` 和 `sync:write`。

关闭时仅 `/v1/inventory` 免JWT，并以固定 `inventory-local-operator` 身份记录任务与分页游标；用户注册/登录/个人资料接口的原规则不变。无需配置grants，但 `inventory.cursor_key` 仍必须是至少32字节的私密随机值。启动会记录免鉴权警告；任何能访问API的人都能创建或取消同步，只能用于受控本地或内网。

本地忽略配置 `config/local.yml` 已设置 `auth.enabled: false` 并独立生成游标签名密钥；提交的示例配置保持开启。修改开关后必须重启HTTP服务。开启时Web会提示鉴权失败，第一期没有登录页面，也不注入测试Token。

```yaml
inventory:
  auth:
    enabled: true # 仅受控开发环境设为false
  cursor_key: "<private-random-value-at-least-32-bytes>"
  grants:
    - user_id: "<exact-JWT-user-id>"
      permissions: ["sync:read", "sync:write"]
```

`source_id` 指向当前已启用来源。`full` 表示完整执行该来源配置下的15阶段，不代表自动采集全部数据中心。来源增删改不在本期范围。

## 接口清单

| 方法 | `/v1/inventory` 下的路径 | 用途 |
| --- | --- | --- |
| POST | `/sync-rounds` | 持久化入队，必需 `Idempotency-Key`，返回202及Location |
| GET | `/sync-rounds` | 分页列表，支持limit、cursor、source_id、status |
| GET | `/sync-rounds/{id}` | 状态、共享时间、15阶段进度、子任务ID及计数 |
| POST | `/sync-rounds/{id}/cancel` | 排队立即取消，执行中协作取消；200或202 |
| GET | `/sync-rounds/{id}/diagnostics` | 整轮及子任务的脱敏诊断 |
| GET | `/sync-runs/{run_id}/diagnostics` | 单阶段任务的脱敏诊断 |

诊断分页接受 `limit`、`cursor`、`source_id`，不接受任务状态筛选。默认每页50条，最多200条；拒绝未知、空值或重复的查询参数。签名游标绑定用户、父任务及筛选，不能跨任务复用；修改分页大小不改变筛选绑定。

创建整轮任务示例（替换为有效JWT及来源ID）：

```http
POST /v1/inventory/sync-rounds HTTP/1.1
Authorization: Bearer <JWT>
Content-Type: application/json
Idempotency-Key: manual-round-2026-11-03-1

{"source_id":"00000000000000000000000000000238","mode":"full"}
```

请求体最多64KiB，只接受 `source_id` 和 `mode`。重复或未知JSON字段、null/非字符串值、尾随JSON、非full模式及非法ID均拒绝。ID是32位小写十六进制；幂等键为1–128个可打印ASCII字符。同来源、同幂等键和相同请求重复提交返回原任务；不同请求复用幂等键，或同来源已有未完成整轮/单阶段任务，返回409。

```http
HTTP/1.1 202 Accepted
Location: /v1/inventory/sync-rounds/<round_id>
Content-Type: application/json

{"code":0,"message":"ok","data":{"id":"<round_id>","status":"queued","total_phases":15,"completed_phases":0}}
```

响应示例省略了部分字段。实际响应包含固定15阶段、子任务ID、开始/结束时间、共享快照时间、计数及脱敏错误码。`completed_phases` 只统计成功阶段，**不是CMDB对象采集百分比**；阶段运行时计数尚未完成，不能将零值理解为来源为空。

执行顺序：data_center、cabinet、device、transformer、ups_group、ups、rpp、pod、interface、power_relations、pod_membership、spatial_relations、gpu_sync、server_uplinks、links_to。整轮持有实时续租的独立租约；子任务创建时就绑定当前阶段，普通worker不能抢领，历史单阶段任务不能用作本轮的完成证明。

执行中取消会在worker观察到请求后阻止后续阶段，不回滚已写入的图数据。租约丢失也会停止当前阶段；已持久化失败或取消的任务不会阻塞后续队列任务。控制库不可用时worker返回错误，不能假装完成。

公开诊断保留对象标识、字段路径、级别及错误码；不返回任意 `Detail` 原文、凭据引用、租约、幂等键、请求哈希或内部错误。

使用既有响应封装：400参数错误、401未登录、403权限不足、404任务不存在、409状态/幂等冲突、504请求超时。浏览器可读取 `X-Request-ID` 和 `Location` 响应头。

Swagger通过 `make swag` 生成，Wire通过 `go generate ./cmd/server/wire` 生成。旧 `/sources` 及单阶段 `/sync-runs` 接口保持原语义。

## 采集性能与日志

端口和GPU使用来源配置中的 `concurrency` 控制设备请求并发，单设备返回页为1000条；超过1000仍继续分页，并检查总数、身份和返回完整性。端口首页同时提供count和记录，不再额外请求limit=1的计数页；GPU上联按服务器 `device_sn` 请求，再匹配卡SN。

当前用户批准的流程：一次完整采集（含分页/目录查询），身份/全局冲突/端点校验→写图→独立查图库验收。worker记录来源采集、校验、图写入及独立查图库验收的分段耗时和累计写入量；写图后不再次读取 CMDB，不以第二遍来源差异产生 `source_changed`。独立只读调试测试可比较两次来源调用，但不属于生产同步流程。日志不包含请求凭据或CMDB原始负载；错误保留明确阶段和脱敏原因码，部分写入保留、不自动重试。GPU失败统计通过已绑定子任务记录到阶段（失败仍为failed），日志进度不是API对象采集百分比。

单遍改造已通过全量Go测试、adapter/service/repository/worker竞态检查、vet及三个入口构建。新增回归明确断言实体来源页面/接口pass只读取一次，GPU及各关系任务不再执行写后复读；首次分页、身份、冲突、端点及写图错误仍失败，GPU执行器的图库验收回调仍必需。没有重跑真实同步或改写历史任务，已有worker需要重启才能加载新代码。

### 端口名规范化与未匹配日志

端口名字匹配和保存统一去除全部Unicode空白，含内部空格、tab；仅作用于接口port_name、GPU上联tor_port/gpu_port、GPU卡conn_ports及服务器上联端口名，SN、UUID和槽位不改。源码有中文注释说明CMDB接口格式差异；规范化后同名或冲突UUID仍拒绝任意匹配。

下一次使用重新编译的worker完整同步时，接口阶段会写入无空白名称，GPU阶段使用相同规范化规则。现有图库不在代码修改期间批量改写；不用只重跑GPU绕过接口名称更新及最终图库验收。

GPU每条未匹配上联输出 `GPU uplink unmatched` 结构化日志，字段包含任务/来源ID、服务器SN、GPU SN/端口、ToR SN、原始和规范化ToR端口名、来源关系ID及原因；不采样、不限制日志条数，不打印认证信息或CMDB原始负载。汇总诊断计数仍保留。日志实际路径沿用本地worker配置，控制台重定向日志也可查看；本次不自动重跑任务或新增开发报告。

### 历史双遍执行记录（2026-11-06）

以下记录保留旧worker实际执行的双遍来源及失败结果，不改写历史；该流程已被上述一次完整采集契约取代，不是当前生产要求。本次文档对齐未重新执行live测试。

2026-11-06真实API任务 `f30d0ea166904acd8c37fbbbbc7b84ba` 测试：端口阶段3分20秒，149542节点；GPU阶段4分37秒，相比此前按服务器串行9分05秒进一步缩短。GPU首次采集60.05秒、端点检查24.69秒、节点写入53.16秒、包含关系60.74秒、上联17.63秒、独立复读60.90秒。

本次GPU仍未完成验收：错误码 `gpu_source_changed`，两遍原始来源摘要不同，但两遍均为20197张有效卡、5341条有效上联、28738条来源对象，不能据此说卡数量变化或请求超时。失败发生在独立复读核对，已写20197节点和25538条边保留；具体诊断已在MySQL/API可读，后两阶段未执行，不自动重跑。该真实任务使用补失败计数映射前的worker，页面原计数为0；后续worker已补失败部分计数回归，未改写历史任务。

## 本轮验证记录（2026-11-03）

- 全仓测试、受影响API/worker/控制库包竞态测试、`go vet ./...`、server/migration/worker三个入口构建及diff检查均通过。
- SQLite真实控制库集成测试通过：授权入队及幂等重放→worker执行15阶段→子任务绑定/禁止普通worker抢领→持久化进度与诊断。执行回调使用测试替身，没有调用真实CMDB或图库。
- JWT/权限、严格JSON、分页游标、取消边界、租约丢失、在途续租取消、队列失败恢复均有回归测试。独立审查发现的历史任务认领、来源隔离及诊断归属问题已修复并验证。
- 经独立审批，本地MySQL增量迁移版本3已经应用；只读验证状态为applied，脚本SHA256与迁移账本一致，管理列表查询成功（当前整轮任务0条）。部署其他数据库仍须自行审批执行迁移。
- 上述2026-11-03记录为开发阶段验证，后续真实HTTP/MySQL联调见下。

## 真实HTTP/MySQL联调（2026-11-04）

- `TestLiveMySQLHTTPFullSync` 启动临时TCP HTTP服务，使用真实路由、JWT、service/repository及本地MySQL；通过API创建任务，再调用真实队列worker采集CMDB并写本地图库。没有使用SQLite或采集替身；测试结束后临时服务关闭，不代表已常驻部署。
- 鉴权401/403、非法参数400、创建202、幂等重放、同来源冲突409、排队取消200、进度200及诊断查询通过。联调发现排队取消完成却返回202的问题已修复，补HTTP和仓储回归。
- 首轮 `949c1dc63b1c412c9150d53892df2c27` 前14阶段成功，links_to采集/写图阶段失败；失败状态及子任务正常保留，没有自动清理或回滚。控制面现有错误码较粗，未断言具体来源或图写入错误原因；之后只读双遍来源及端点检查通过。
- 重试整轮 `5b5c81f44b1544de8d42da14d0d86125` 成功，15/15阶段验收完成，HTTP可读取持久化进度及16条汇总诊断。GPU1289、GPU上联455、服务器上联1657、空间关系5636、设备POD1445、物理连接3026；数量反映本次来源，不沿用旧快照。
- 保留本地MySQL测试任务历史，不新增开发报告文件；没有清理旧图数据，也没有改动配置文件或实际用户权限。测试JWT/权限仅存在于临时HTTP服务进程。
