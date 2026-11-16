# S0+S1 垂直切片交付记录

## 身份与状态

- 日期与任务：2026-11-16 深夜，S0 建馆 + S1 查询垂直切片。
- 状态：**代码已实现并通过全部测试与真实运行验证**；未提交前工作区可见全部 diff。
- 基线：docs/okb-foundation 分支，v2 基线文档（411bbc3）之后。
- 授权：用户口头授权通宵实现（"现在就开始逐步实现，明早验收，期间权限都批准"）。

## 已实现

- **Go module**：`go.mod` 初始化；依赖 gorm + glebarez/sqlite（测试）+ gorm/mysql（生产），标准库 HTTP（零 web 框架依赖）。
- **S0 建馆**：
  - `internal/model`：v2 三层数据模型（不可变层 source/source_version/parsed_version/fragment；图谱层 anomaly_type/anomaly_alias/causal_rule/alert_mapping；证据修订层 evidence/revision/feedback）。
  - `internal/repository`：全部实体读写；`GetOrCreate*` 幂等接纳；`AddEvidence` 按独立 SourceID 去重计数；`AddFeedback` 唯一索引幂等。
  - `internal/extractor`：Extractor 接口 + FakeExtract（识别 JSON 标记片段，不捏造）；`Validate()` 拒绝缺失字段候选。
  - `internal/service/ingest.go`：脱敏门禁（IP/手机号/邮箱/身份证/凭据/MAC 六类模式，拒绝即 422 不入库）→ 内容寻址版本 → 片段切块 → 提取 → 候选接纳 → **≥2 独立工单印证自动升 curated**（边与映射同规则）。
- **S1 查询**：
  - `internal/service/query.go`：lookup（置信度排序）、trace（DFS 遍历+环检测+跳数/条数有界、受控条件求值 passed/failed/unknown——failed 边不可走、unknown 作为待验证检查项、组合链标注"未逐链审核"）、evidence（fragment 级权限：未授权只见摘要）。
  - `internal/service/feedback.go`：report 幂等反馈（confirmed +0.05 / rejected −0.10 降 draft 入复核队列）、ReviewQueue、ConfirmEdge/RejectEdge 专家治理、别名确认。
  - `internal/handler` + `cmd/server`：REST 全端点（lookup/trace/evidence/report/sources/review/queue/edges/{id}/confirm|reject/healthz）；权限表 OKB_AUTHZ= id:level（0 仅摘要 / 1 读原文 / 2 审核+include_draft）。
- **测试**：`test/integration/api_test.go` 覆盖 A1—A7 全部验收场景。

## 验证证据（真实运行）

- `go build ./...`：通过。
- `go test ./...`：**ok UnifiedInfraOKB/internal/... ok UnifiedInfraOKB/test/integration**（A1 摄取回链 / A2 脱敏门禁 / A3 印证计数 / A4 lookup+trace+条件求值 / A5 反馈幂等+复核队列+修订追加 / A6 fragment 权限 / A7 节点对齐，全部 PASS）。
- SQLite 真实 HTTP 冒烟（:18090）：两份合成工单摄取 → 第 2 份触发 CuratedRules:1；lookup 默认返回 curated 候选（含 revision_id）；trace 返回 A→B 候选链（case_verified、条件求值、钉 revision）；evidence 匿名只见摘要、agent-1（level 1）见全文；report confirmed→0.55、重复上报 recorded:false、rejected→draft+复核队列；expert-1 确认恢复 curated。
- MySQL 真实冒烟（本机 MySQL 独立 `okb_test` 库，:18091）：AutoMigrate 建 11 表成功、摄取成功、lookup 权限行为正确（anonymous 无审核权限不见 draft）。**未动 unified 拓扑库**。
- 发现并修复 2 个真实 bug：① AddFeedback 幂等判断写反（Count 永真）→ 改用 RowsAffected；② alert_mapping 无印证升级 → 补齐同规则。

## 未实现 / 已知限制

- FakeExtract 是固定适配器：只识别合成 JSON 标记片段；**真实 LLM 提取器未接**（需获准模型配置，S0 后续任务）。
- `inferred`/`expert_only` 边的专家确认流程有 service/handler，无前端界面（S2）。
- 抽样审核（10%）字段已建模（Config.SampleRate），抽样动作未实现。
- migrations/ 目录占位未写 DDL 脚本：生产用 AutoMigrate 或后续补 0001_init。
- time_constraints 传入返回但未参与求值（无场景需求，S3 按需）。
- cmd/worker 占位未实现（当前摄取同步执行）。

## 环境备注

- 本机沙盒拦截 macOS 钥匙串 TLS 验证与默认 go-build 缓存，go get/test/build 均需提升权限执行（用户已批准）。
- 本机已有 MySQL 8.0.31（root:123456@127.0.0.1:3306），已建独立 `okb`/`okb_test` 库；未安装任何新软件。

## 下一步建议

1. 用户验收本切片 diff 与运行证据。
2. 真实 LLM 提取器接入（extractor 接口已就位，换实现即可）。
3. S2：专家最小界面（UnifiedInfraWeb 清单内）+ 抽样审核动作。
4. 补 migrations DDL 脚本，生产改用脚本迁移（OKB_AUTO_MIGRATE=false）。
