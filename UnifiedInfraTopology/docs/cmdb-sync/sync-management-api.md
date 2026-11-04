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

所有接口要求JWT。授权用户ID及权限区分大小写；读取要求 `sync:read`，创建及取消任务同时要求 `sync:read` 和 `sync:write`。`inventory.cursor_key` 必须是至少32字节的私密随机值，不得泄露该值或来源凭据引用。

```yaml
inventory:
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

## 本轮验证记录（2026-11-03）

- 全仓测试、受影响API/worker/控制库包竞态测试、`go vet ./...`、server/migration/worker三个入口构建及diff检查均通过。
- SQLite真实控制库集成测试通过：授权入队及幂等重放→worker执行15阶段→子任务绑定/禁止普通worker抢领→持久化进度与诊断。执行回调使用测试替身，没有调用真实CMDB或图库。
- JWT/权限、严格JSON、分页游标、取消边界、租约丢失、在途续租取消、队列失败恢复均有回归测试。独立审查发现的历史任务认领、来源隔离及诊断归属问题已修复并验证。
- 经独立审批，本地MySQL增量迁移版本3已经应用；只读验证状态为applied，脚本SHA256与迁移账本一致，管理列表查询成功（当前整轮任务0条）。部署其他数据库仍须自行审批执行迁移。
- 本次没有创建真实整轮任务、访问CMDB、改动图库、启动持续HTTP服务或队列worker；不宣称已经做过HTTP发起的真实全量采集。准备联调时按上方命令分别启动服务和worker，并配置实际JWT用户的sync权限。
