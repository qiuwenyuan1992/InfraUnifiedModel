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

`/v1/scopes` 提供经身份认证的作用域、设备、接口、地址、来源和已发布批次读取，
以及持久化同步任务的创建、列表、详情和取消；已有用户接口仍保留。
同步任务**只入队，不执行**。尚无采集 worker、发布流水线、拓扑查询或上游写入。
证据、来源进度、检查点和覆盖摘要尚未开放；生成的 Swagger 仅覆盖已有用户路由，
不是完整的 inventory API 文档。

创建同步任务需要认证、`Content-Type: application/json` 和 `Idempotency-Key` 请求头。
请求接受 `source_ids`、`mode: "full"` 以及可空的 `base_generation_id`，返回 HTTP 202 和 `Location`。
相同规范化请求体与幂等键重放时返回原任务；创建成功不代表已执行。

资产读取要求作用域为 `ready`，且当前已发布批次的 inventory 与 graph 均就绪，
否则返回 `projection_not_ready`。`generation_id` 只能选择当前批次；非当前批次返回冲突，不能读取历史。
资产 ID 为 32 位小写十六进制字符串。

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

资产控制数据使用 `data.db.main`。配置真实用户/作用域 ID，并独立生成至少 32 字节的游标签名密钥，
不得复用 JWT 密钥：

```yaml
inventory:
  cursor_key: "REPLACE_WITH_DEPLOYMENT_MANAGED_SECRET"
  grants:
    - user_id: "authenticated-user-id"
      scope_id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      permissions: ["inventory:read", "sync:read", "sync:write"]
```

没有默认授权。作用域和来源记录由部署方管理，不提供种子数据或配置 CRUD 接口。
缺少游标配置时列表操作不可用。v2 游标绑定调用方、作用域、资源、过滤条件、当前发布批次和投影 epoch；
旧格式游标、非当前批次选择器以及图更新后失效的游标不能读取历史资产，必须从当前批次重新分页。
轮换签名密钥也会使已有游标失效。

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

## 当前图存储与初始化

Nebula 保存当前设备、接口、地址和关系；MySQL 保存控制与身份元数据，不保存逐批次资产副本。
VID 为 `scope_id + ':d:' / ':i:' / ':a:' + entity_id`，固定 67 字节，空间类型为 `FIXED_STRING(67)`；
VID 不包含 `generation_id`。原有 `0000`/`0001` 迁移及校验和保持不变，
`0002` 增加投影状态与 epoch。旧资产表不再使用，但不会自动删除。
来源历史归属外部 CMDB ClickHouse，尚无历史适配器；本节优先于早期 foundation 计划中的快照读取假设。

审阅 `docs/schema/current_graph.ngql`，仅在**新建专用空间**逐阶段手工执行，禁止用于遗留空间。
先审阅分区和副本设置，再按顺序操作：

1. 创建专用空间，等待元数据传播。
2. 选择该空间并创建标签与边类型，等待元数据传播。
3. 创建索引，等待索引可用后再接入读取服务；导入已有数据后的索引重建需单独运维审核。

配置中的空间名必须与实际空间一致：

```yaml
inventory:
  # 与上面的 cursor_key/grants 合并，不要重复定义 inventory。
  graph:
    hosts: ["127.0.0.1:9669"]
    space: "unified_inventory_current"
    username: "DEPLOYMENT_USER"
    password: "DEPLOYMENT_SECRET"
    timeout_seconds: 5
```

启动永不创建图 schema。缺少 hosts 或 space 时资产读取不可用且不会回退到 SQL，控制接口仍可启动。
显式配置错误或图服务不可达可能导致启动失败。SDK socket 超时只约束单次网络操作，
不是整个 HTTP 请求的截止时间；取消检查在 SDK 调用前后执行，不能中断进行中的调用。

作用域初始为 `uninitialized`。未来发布器必须在**任何图修改之前**持久化 `updating` 状态并递增
单调 epoch，完成并验证全部图写入后，再在 MySQL 中原子发布当前批次和 `ready` 状态。
失败必须保持不可用，不能在部分写入后恢复就绪。读取端在图访问后复查状态、epoch 和当前批次。
这是写入隔离协议，不是分布式事务；没有发布器或 CMDB 采集 worker，建好 schema 不会导入资产或使作用域就绪。

图中地址为文本 IP，读取后转换为领域字节表示；IPv4 内部为映射的 16 字节编码，JSON 输出点分十进制，
IPv6 保持 IPv6。列表限制只约束返回量，生产前须用真实规模数据执行 EXPLAIN/PROFILE 检查扫描与排序成本；
尚未证明百万接口规模的性能。

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
实时图检查默认跳过，仅在专用开发空间显式启用，配置路径应为绝对路径：

```sh
INVENTORY_GRAPH_TEST_CONFIG=/absolute/path/to/development.yml \
  go test ./internal/repository -run '^TestGraphInventoryLiveReadOnly$' -count=1
```

该测试只读检查 `unified_inventory_current` 的 schema 和查询。不要指向生产环境，禁止为验证执行生产写入。
执行前先初始化专用空间的 schema；常规测试不会验证真实环境。
`make test`、`make coverage` 和 `make race` 显式关闭实时检查；需使用上面的直接命令单独启用。
实时 MySQL DDL 兼容性、大规模图性能及生产部署仍未验证。
更多契约见 `docs/schema/current_graph.ngql` 和 `docs/superpowers/` 下的 API/存储文档。

## 许可证

采用 MIT 许可证，详见 [LICENSE](LICENSE)。
