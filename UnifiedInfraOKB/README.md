# UnifiedInfraOKB 故障知识图谱图书馆

UnifiedInfraOKB 是 InfraUnifiedModel 中独立后端模块，定位为**故障知识图谱与 Agent 诊断记忆底座**：结构化保存带证据出处的因果知识，供诊断 Agent（REST 四原语）与运维专家（极简界面）消费。目标参照 XiHe, *Networked Agent Memory and Causality Representation: Experiences towards Interpretable Cloud-Scale Root-Causing*, SIGCOMM 2026。

**S0 建馆 + S1 查询切片已实现并通过测试与真实运行验证（交付记录见 docs/development/handoffs/）。真实 LLM 提取器、专家界面（S2）与迁移 DDL 尚未实现。**

## 从哪里开始

1. [模块协作约束](AGENTS.md)
2. [v2 需求基线](docs/design/requirements.md)：定位、证据分级、准确性三层机制、切片 S0—S3、验收 A1—A7
3. [数据与存储模型](docs/design/storage-model.md)
4. [REST API 边界](docs/design/api-boundaries.md)：lookup / trace / evidence / report
5. [开发接手入口](docs/development/README.md)

v1 全部设计文档已删除，如遇残留旧设计描述一律以 v2 基线为准。

## 核心资产

```
节点   anomaly_type      异常（stable_code 唯一，厂商别名归并）
边     causal_rule       源异常 → 目标异常：触发条件、时间约束、反证、证据等级
入口   alert_mapping     告警 signature → 候选异常（置信度）
```

知识按需查询、不进 prompt；每条边可回链到工单/文档原文片段（verifiable interpretability）。

## 数据与准确性

- **数据源**：脱敏故障工单/复盘为一等公民（结构化程度低，LLM 二次结构化提取）；厂商文档与专家经验为补充源。
- **证据分级**：case_verified > doc_explicit > inferred > expert_only，生来自源、不事后提升。
- **准确性三层**：来源定级 → 抽样审核（10%）+ ≥2 独立工单印证升 curated → 运行时反馈飞轮（report 确认/否决回写置信度，否决入库为反证）。
- **两态治理**：draft → curated，修订历史追加式。

## REST 四原语

| 原语 | 用途 |
| --- | --- |
| `lookup(alert_signature)` | 告警 → 候选异常 |
| `trace(anomaly_id, facts?, max_hops, top_k)` | 候选因果链，每跳带条件求值与证据等级 |
| `evidence(id)` | 出处回链（原文直返；权限分级后置 S3） |
| `report(diagnosis_ref, used_edges, verdict)` | 反馈飞轮入口，幂等 |

## 交付切片

| 切片 | 内容 |
| --- | --- |
| S0 建馆 | 案例摄取 + LLM 结构化提取 + 证据回链 + 图存储 + 两态修订 |
| S1 查询 | REST 四原语 + 条件求值（先建馆后查询） |
| S2 治理闭环 | 反馈回写 + 专家最小界面 |
| S3 按需 | NebulaGraph 导出、多人审核、权限分级、MCP 包装等真实需求出现后立项 |

## 已确认边界

- 同仓库独立 Go 后端；独立 MySQL 知识库 + 文件/对象存储；不碰 UnifiedInfraTopology / TopologyDetails.vue / CMDB。
- OKB 是被动应答的图书馆，不做诊断编排，不保存会话状态；会话级工作记忆归未来 Agent 编排层。
- REST 即可，本期不做 MCP；查询输入是告警 signature / 异常 stable_code，不是资产 ID。
- 资料原文、模型原始响应、真实工单正文、凭据不进 Git；工单入库前过脱敏校验（sensitivity_cleared）。

## 目录职责

| 路径 | 职责与当前状态 |
| --- | --- |
| cmd/server、cmd/worker | 未来 HTTP API 与提取 worker 入口；当前占位 |
| api/v1 | 未来 REST 契约与类型；当前占位 |
| internal/handler、service、repository、model | 未来传输、业务、持久化、领域代码；当前占位 |
| internal/document、extractor、storage、authorization、worker | 解析、模型适配、文件存储、授权、后台执行边界；当前占位 |
| migrations/mysql | 独立知识库迁移；当前没有 SQL |
| test/fixtures/synthetic、test/integration | 合成案例与未来集成验证；当前占位 |
| config | 未来无秘密配置示例；当前占位 |
| docs/design | v2 设计基线 |
| docs/development | 接手入口、模板与交付记录 |

## 验证与运行

本轮仅能核对文档链接、目录与 Git 改动。应用启动、go test、模型提取、MySQL 迁移及 Web 联调均尚未可执行；未来任务初始化后记录真实命令与输出。
