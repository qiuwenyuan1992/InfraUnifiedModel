# GPU parts_sn 身份实施计划

**状态：** 设计已由用户于 2026-11-02 上一轮批准，无需再次批准。
**设计：** `docs/superpowers/specs/2026-11-02-gpu-parts-sn-design.md`

## 约束与验收

当前用户批准的同步流程：一次完整采集（含分页/目录查询），身份/全局冲突/端点校验→写图→独立查图库验收。写图后不再次读取 CMDB，不以第二遍来源差异产生 `source_changed`；独立只读调试测试可比较两次来源调用，但不属于生产同步流程。

唯一 GPU 身份为 `source_id + gpu + parts_sn`；忽略缺失/空白 SN，不回退到卡 `uuid` 或 `inst_id`，不持久化卡 UUID，不双写。2026-11-06 查询契约更正：先按服务器 `condition.device_sn` 完整分页采集卡并识别有效/冲突 SN，再对有有效卡的服务器按 `condition.device_sn` 完整分页查询上联，不逐卡请求；返回的 `gpu_sn` 仅解析同一服务器所属的有效 `parts_sn`，缺失、未知 SN 或归属/属性冲突的记录跳过，没有有效卡的服务器不请求上联。`gpu_uplink.uuid` 保持来源关系身份。实施覆盖模型/规则/映射文档、Schema 基线及增量迁移、适配器、repository、CLI 与对应测试；保留已有脏工作区。不提交；live DDL 必须独立审批，且GPU节点存量为0。

## 顺序

> 以下已完成步骤及 RED/GREEN、数量和 live 验收为历史记录，不代表本次更正重新执行了测试或采集。历史实现曾逐卡请求并在写后复读核对两遍来源摘要；这些旧行为已被 2026-11-06 按服务器 `condition.device_sn` 完整分页查询及上述一次完整采集契约取代，不是当前生产要求。

- [x] 阅读 scoped instructions 与当前 GPU 契约，记录用户已批准设计。
- [x] 修改 `internal/migration/nebula_schema_test.go` 的 GPU 字段和索引断言，并增加独立增量迁移断言；修改基线前运行 RED。
  - RED：`GOCACHE=/tmp/gpu-schema-go-cache go test ./internal/migration -run 'TestNebula(SchemaContract|GPUPartsSNIdentityMigration)$' -count=1 -timeout=60s`；失败原因为多余 `uuid` 和缺少 `0004_gpu_parts_sn_identity.ngql`。
- [x] 将基线 GPU 字段改为 `parts_sn STRING NOT NULL` 并删除 `uuid`；索引改用 `parts_sn`。
- [x] 添加独立 `0004_gpu_parts_sn_identity.ngql`；执行前确认 GPU 节点数为 0，人工批准后才可执行。说明 CHANGE NOT NULL 所需 DEFAULT 不是空 SN 身份回退。
- [x] 同步 GPU、composition、runtime contract、AGENTS 与 CMDB mapping 文档，保留上联来源关系 UUID。
- [x] 运行 migration 包测试、包级 vet/build 和 `git diff --check`，记录 GREEN 证据；全仓集成验证由父任务执行。
  - GREEN：`GOCACHE=/tmp/gpu-schema-go-cache go test ./internal/migration -count=1 -timeout=60s`，包测试通过。
  - `GOCACHE=/tmp/gpu-schema-go-cache go vet ./internal/migration`、`GOCACHE=/tmp/gpu-schema-go-cache go build ./internal/migration`、`git diff --check` 均退出码 0。
  - 默认 Go build cache 的首次 RED 命令超时无输出；使用 `/tmp/gpu-schema-go-cache` 后获得上述 RED/GREEN 证据。未执行任何 live DDL 或 Git 提交。
- [x] 适配器/服务新增SN身份、缺SN跳过、逐卡请求及端点回归，确认RED后修改 `cmdb_gpu.go`；全局同SN归属/属性冲突阻止写入。
- [x] repository稳定字段及CLI查图新增RED断言，随后改用 `parts_sn`；样例JSON不再输出GPU UUID。
- [x] 独立审查发现上联去重可能绕过当前查询SN校验；新增多卡回归确认RED，移动校验至去重之前并确认GREEN。
- [x] 全仓 `go test ./... -count=1 -timeout=90s`、adapter/service/repository/worker竞态测试、`go vet ./...`、三个入口构建及diff检查通过。
- [x] 只读IDC238全范围检查：3287条卡记录，1992条无SN，1292个唯一SN，3个SN存在跨服务器归属冲突；记录诊断至 （历史开发报告已清理）。
- [x] 用户确认全部排除冲突SN：先增加跨服务器冲突、同服务器属性冲突及再次重复的RED测试，随后两阶段采集实现全量冲突识别、跳过全部记录且不查询上联；无冲突卡正常保留，GPU回归GREEN。
- [x] 独立审批执行GPU Schema迁移：确认全图GPU、contains_gpu、gpu_uplink均为0，替换身份字段/索引为parts_sn并只读复核。
- [x] 实际GPU发布及独立验收：首个SN任务完成节点/边写入但最终阶段失败，保留诊断不回滚；只读两遍来源摘要一致后幂等重跑，任务 `9c7fe3fc7799cd778a7a44f3d273922f` 成功、检查点完成、租约释放。节点1289、contains_gpu1289、gpu_uplink167；1992条缺SN、6条冲突SN记录排除，288条不能唯一解析ToR端口的上联诊断跳过，不宣称全覆盖。
