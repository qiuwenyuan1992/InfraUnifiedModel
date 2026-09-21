# UnifiedInfraTopology

基于 Go 和 Nunu 分层布局的基础设施资产 API：MySQL 保存控制元数据，NebulaGraph 保存当前资产。
推荐阅读本中文说明；[English](README.md)。

## 目录与职责

| 路径 | 职责 |
| --- | --- |
| `cmd/server` | HTTP API 入口与依赖装配 |
| `cmd/migration` | 运维显式执行的 SQL 迁移入口 |
| `api/v1` | 版本化请求、响应 DTO 与 API 契约 |
| `internal/handler` | HTTP 传输、参数绑定和响应映射 |
| `internal/service` | 业务规则、权限校验与流程编排 |
| `internal/repository` | MySQL 控制/身份持久化与 Nebula 当前资产访问 |
| `internal/model` | 领域与持久化模型 |
| `internal/migration` | 版本化 SQL 与迁移账本逻辑 |
| `internal/server` | HTTP 服务构建与路由 |
| `pkg` | 配置、日志、JWT 和图客户端等基础设施 |
| `*_test.go`、`test` | 同目录测试、已有服务测试与 `test/mocks` |

旧 task/job 示例已移除，现有基础结构职责如下：

- `cmd/worker`：独立进程入口及 Wire 装配，加载配置，响应 SIGINT/SIGTERM 优雅退出。
- `internal/adapter`：外部来源接入边界；`SourceAdapter.Validate` 定义接入前检查，CMDB 占位实现返回 `ErrNotImplemented`。
- `internal/service/sync_worker.go`：同步编排入口；`Run` 仅待机，`Execute` 明确返回未实现，不会报告同步成功。

启动基础版（可直接使用脱敏模板，无需数据库凭据）：

```sh
make worker CONF=config/local.example.yml
# 或直接运行入口，按 Ctrl+C 退出
go run ./cmd/worker -conf config/local.example.yml
```

`APP_CONF` 优先于 `CONF` / `-conf`。`make build` 包含 worker，`make wire` 包含其依赖装配。
当前 worker 不启动 HTTP、不连接 MySQL/Nebula、不执行迁移、不领取或修改队列任务。
真实采集、发布、来源配置解析和任务租约尚未实现；接入时再定义采集结果契约，不伪造资产或成功状态。

## 已实现的 API

`/v1/inventory` 当前提供经身份认证的来源列表，以及同步任务的创建、列表、详情和取消；已有用户接口仍保留。
同步任务**只入队，不执行**。尚无资产读取、采集 worker、发布流水线、拓扑查询或上游写入。
证据、来源进度、检查点和覆盖摘要尚未开放；生成的 Swagger 仅覆盖已有用户路由，不是完整的 inventory API 文档。

创建同步任务需要认证、`Content-Type: application/json` 和唯一的 `Idempotency-Key` 请求头。
请求接受单个 `source_id` 和 `mode: "full"`，返回 HTTP 202 和 `Location`。
相同规范化请求体与幂等键重放时返回原任务；创建成功不代表已执行。
资源 ID 为 32 位小写十六进制字符串。列表游标版本为 v1，绑定调用方、资源和过滤条件；轮换至少 32 字节的独立游标签名密钥会使已有游标失效。

## 配置与启动

在本模块目录执行命令。首次本地配置：

```sh
cp config/local.example.yml config/local.yml
```

**已有配置请原样保留，不要用示例覆盖。** `config/local.example.yml` 和
`config/prod.example.yml` 仅包含脱敏占位值；使用前填写数据库凭据和强 JWT 密钥。
本地 `config/local.yml`、`config/prod.yml`、`storage/nunu-test.db` 及运行产物被 Git 忽略，不能作为部署制品；
取消跟踪不会删除磁盘上的已有文件。历史提交中的默认值和密钥仍在 Git 历史中，
应轮换已暴露凭据，生产环境禁止沿用默认凭据。

资产控制数据使用 `data.db.main`。配置真实用户 ID，并独立生成至少 32 字节的游标签名密钥，
不得复用 JWT 密钥：

```yaml
inventory:
  cursor_key: "REPLACE_WITH_DEPLOYMENT_MANAGED_SECRET"
  grants:
    - user_id: "authenticated-user-id"
      permissions: ["sync:read", "sync:write"]
```

没有默认授权。来源记录由部署方管理，不提供种子数据或配置 CRUD 接口。
缺少游标配置时列表操作不可用。v1 游标绑定调用方、资源和过滤条件；轮换签名密钥会使已有游标失效。

**核实实际目标配置并备份目标数据库后**，显式执行：

```sh
go run ./cmd/migration -conf config/local.yml
go run ./cmd/server -conf config/local.yml
```

设置 `APP_CONF` 时，它会覆盖 `-conf`；执行任一命令前请检查或取消该环境变量。
没有自动 bootstrap；API 启动不执行 SQL 迁移或图 DDL。
迁移命令执行 inventory schema 和已有用户模型迁移后退出，不由 API 进程调用。
版本和校验和记录在 `topology_schema_migrations`；不要修改已应用 SQL，也不要清除 dirty 账本强行重试。
失败后应核对账本与实际 schema，恢复备份或完成运维审核的修复后再执行。
MySQL DDL 可能部分提交，SQLite 回滚测试不代表 MySQL 兼容性已验证。
SQLite 部署/测试必须为**每个池连接**开启外键，例如使用驱动支持的
`file:inventory.db?_pragma=foreign_keys(1)` DSN。

## 模型开发基线

当前 NebulaGraph 基线为 `internal/migration/nebula/0001_topology.ngql`，由 `docs/model/` 下的权威实体与关系定义生成：

- Space：`unified_infra_topology`。
- 10 个 Tag：`device`、`interface`、`gpu`、`pod`、`cabinet`、`data_center`、`rpp`、`ups_group`、`ups`、`transformer`。
- 4 个 Edge Type：`spatial_relation`、`composition_relation`、`network_relation`、`power_relation`；具体语义由 `relation_kind` 区分。

server 和 worker 不自动执行图 DDL。Schema 必须由运维在专用 Space 显式执行，并检查每条语句的 Nebula 返回结果。首次初始化分三阶段执行：创建 Space 后等待至少两个 heartbeat 周期，再执行 `USE`、Tag 和 Edge；随后再等待至少两个 heartbeat 周期，最后创建索引。某些 Console 用法会按换行拆分多行语句，不能只依据进程退出码判断成功。

## 开发与验证

```sh
make help
make test   # 完整测试范围：./...
make build
make vet
make race
make wire
make swag   # 仅生成已有用户路由的 Swagger 元数据
```

`make test` 包含同目录测试和已有服务测试；`make wire` 重新生成依赖装配；
`make swag` 不会补齐 inventory 接口文档。常规测试使用隔离 SQLite fixture 与 mock，不连接配置中的数据库。
真实图验收默认跳过，仅在已初始化 `unified_infra_topology` 的专用开发 Space 显式启用，配置路径必须为绝对路径：

```sh
TOPOLOGY_GRAPH_TEST_CONFIG=/absolute/path/to/development.yml \
  go test ./internal/repository \
  -run '^TestTopologyGraphLiveWriteRefreshAndCleanup$' -count=1 -v
```

该测试会真实写入并清理专用验收数据，验证节点、GPU 保留字段、关系、`created_at` 保留、UTC 微秒 `synced_at` 刷新、旧关系删除和孤立节点删除。禁止指向生产环境或共享 Space。
常规测试不会连接真实 NebulaGraph；实时 MySQL DDL 兼容性、大规模图性能及生产部署仍未验证。
当前实体与关系模型统一以 `docs/model/` 为准，历史实施计划和规格不再作为开发依据。

## 许可证

采用 MIT 许可证，详见 [LICENSE](LICENSE)。
