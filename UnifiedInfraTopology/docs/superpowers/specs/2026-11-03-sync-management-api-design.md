# 同步管理API第一期

## 已确认范围

用户决定先开发API，再做Web后台、图谱浏览和故障浏览。第一期不增加来源增删改，不扩展当前IDC238来源筛选，不自动清图，不接入每日调度。保留一个CMDB来源及来源查看，后续全数据中心采集须独立调整来源契约。

全量更新政策已确认：整轮同步及验收成功后允许清理旧数据，即使本轮存在缺失或异常跳过；整轮失败不清理。此政策记录但清理执行不是本期范围，不能在API中冒充已实现。

## 方案

复用原单资源sync-runs会使其状态、租约和每阶段完成逻辑混淆；HTTP内启动goroutine又无法跨重启保留任务，因此不采用。新增持久化sync-rounds和固定15阶段控制记录，复用已有单阶段任务作为子任务；独立worker执行，HTTP只入队。

本期由用户“开始吧”指令直接推进，具体实现采用现有Nunu handler→service→repository布局、JWT及sync:read/write权限。

## API

- POST /v1/inventory/sync-rounds：source_id与mode=full，必需Idempotency-Key，202及Location；同来源存在未结束整轮或单阶段任务则409。
- GET /v1/inventory/sync-rounds：分页，source_id/status筛选。
- GET /v1/inventory/sync-rounds/:id：整轮状态、共享时间、15阶段进度、子任务ID、计数及脱敏错误码。阶段完成率不是CMDB对象采集百分比。
- POST /v1/inventory/sync-rounds/:id/cancel：排队立即取消，执行中协作取消并阻止后续阶段；不回滚已写图。
- GET /v1/inventory/sync-rounds/:id/diagnostics：跨子任务的诊断列表；GET /sync-runs/:id/diagnostics供单阶段追查。游标绑定用户、父ID及筛选，不能跨任务复用。
- 现有sources和sync-runs保留原语义，不能把单资源full任务当完整轮次。

## 持久化与执行

MySQL仅新增整轮与阶段的控制状态，不保存图实体或原始CMDB负载。SQL增量迁移MySQL/SQLite对等，不修改旧迁移校验；部署执行迁移独立审批。本期无NebulaDDL。

整轮在source行锁下排队/领取，持有独立租约，单资源入队检查整轮占用。worker用内部上下文标识已领取整轮和当前阶段，只有有效租约的本轮阶段可创建子任务；HTTP不能设置该标识。通用单资源worker不得抢领整轮子任务。

阶段顺序与既有完整轮次相同：九种常规实体，再power_relations、pod_membership、spatial_relations、gpu_sync、server_uplinks、links_to。共享T只用于图写入/检查点/验收，租约使用实时钟。保留CLI独立同步，完整轮次逻辑可供队列worker复用；请求结束不会取消已入队任务。

阶段进度实时持久化，附子任务ID；计数完成后更新。整轮失败保留成功阶段及not_run阶段。中断/租约过期不得静默报成功或清理图数据。所有响应不包含DSN、CMDB凭据、任意错误原文或大体积来源数据。

## 验收

先RED后GREEN；覆盖HTTP鉴权/校验/幂等/游标、并发来源冲突、排队和执行中取消、子任务归属与抢领保护、租约丢失、阶段失败停止、共享时间及统计持久化。验证Wire、接口文档、全量测试、竞态、vet和三个入口构建。默认测试不访问CMDB或图库，不为验证自动启动实际全量同步。
