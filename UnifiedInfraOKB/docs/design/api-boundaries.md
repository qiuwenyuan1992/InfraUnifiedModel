# OKB v2 API 边界：REST 四原语

日期：2026-11-16。**v2.1 修订：2026-11-20**——契约经 v2.1 修订机制变更（授权：用户 2026-11-20 批准"边语义三分"方向）：lookup/trace 增加非因果边（same_event/same_family）的**标注式返回**语义（不参与遍历），lookup 支持结构化 signature。原"接口契约在本文件冻结"改为"接口契约冻结于本文件；变更走修订记录"。接口形态：**REST 即可，本期不做 MCP**。消费者：诊断 Agent（主要）、专家最小界面（复用同一 API）。

## 1 查询原语

### 1.1 lookup —— 告警入口

```
GET /api/v1/lookup?alert_signature=<sig>
GET /api/v1/lookup?domain=<d>&channel=<ch>&sig_key=<k>   （v2.1：结构化 signature）
```

返回：候选异常列表，每项含 anomaly stable_code、display name、confidence、evidence_level、来源计数、revision_id；**v2.1 增**：`severity`（severity_online + severity_dict）、`node_kind`、`clock_hint`，以及两类关联标注（均不进入候选因果链，仅供消费方聚合证据/对比）：
- `same_event_peers`：同一物理事件的另一面（例：服务器 Link Down 查出交换机 Port Down 同事件对端，供"双侧同时告⇒链路本体坏"分流）
- `family_peers`：同族对比候选（例：CRC 双侧对比）

### 1.2 trace —— 候选因果链遍历

```
GET /api/v1/trace?anomaly_id=<id>&max_hops=3&top_k=5&facts=<URL编码的观测事实JSON,可选>
```

- `facts`（可选，F3）：调用方已掌握的观测事实（指标、状态、环境）。
- 返回：top-k 候选根因链；每跳带：边 ID、**edge_kind**、触发条件及求值结果 `passed / failed / unknown`、时间约束（v2.1 含传播延迟档 seconds/minutes/hours）、证据等级、出处 ID、revision_id。
- **v2.1 遍历语义（三分）**：`edge_kind=causal` 的边可遍历前进；`same_event`/`same_family` 的边**不可遍历**——trace 只沿 causal 边构建根因链，非因果关联在响应的 `relations` 区返回（同事件对端/同族对端列表，带对方 severity），消费方用于证据聚合与对比诊断，不推进链。
- `unknown` 的条件作为"待验证检查项"返回，由调用方取证——这是 evidence-driven 诊断的输入。
- 条件不满足（failed）的边不可走；跨跳组合显式标注"逐跳已核 / 组合推导"。
- 输出有界：max_hops 默认 3，top_k 默认 5，防止调用方上下文饱和。**v2.1 场景建议**：跨域级联场景（制冷失效→次生火灾类）实测需 5-6 跳，调用方按场景传 `max_hops=6`；跳数建议值可随 causal_rule 的场景标记（scenario ref）下发。

### 1.3 evidence —— 出处回链

```
GET /api/v1/evidence?id=<node_or_edge_id>&evidence_id=<可选>
```

- 返回：证据列表，每项含 evidence_level、source kind、fragment 回链（摘要与原文）。
- 权限（F4）：本期不启用（2026-11-16 用户决定，后置 S3）——原文直返；S3 启用后按 fragment 级 permission_level 校验，未授权只返回 summary。

### 1.4 report —— 反馈飞轮入口（F3）

```
POST /api/v1/report
{ "diagnosis_ref": "...", "used_edges": ["edge_id..."], "verdict": "confirmed|rejected", "operator": "服务端识别", "notes": "..." }
```

- 语义：上报本次诊断使用的边与最终人工/系统判定。
- 效果：confirmed 升边置信度；rejected 降置信度、降级并进入复核队列，否决记录入库为反证。
- 幂等：同一 diagnosis_ref + edge 重复上报不重复计数。

## 2 管理与摄取接口（S0/S2 切片）

- 案例摄取：`POST /api/v1/sources`（internal_case 上传 → 脱敏校验 → 打 sensitivity_cleared）。
- LLM 结构化提取任务：提交后异步执行，结果以 draft 候选呈现，不直接入图。
- 专家操作：`POST /api/v1/edges/{id}/confirm|reject`、归并建议确认、补充证据。
- 操作者以 Bearer 署名记审计；按动作授权本期不启用（后置 S3）。

## 3 通用约束

- **所有读响应携带 revision_id**（F5）：调用方答案可复现，钉住修订版本。
- 知识按需查询，不提供"全量导出给 prompt"的接口。
- 错误语义：`404`（实体不存在）、`422`（内容/Schema 不合法，含脱敏校验失败）。
- 草稿（draft）知识在 trace/lookup 结果中默认排除，除非显式 `include_draft=true`（本期无审核权限门槛，S3 启用后须审核权限）。
- 接口契约在本文件冻结；实现切片顺序仍为 S0 建馆 → S1 查询（先建馆后查询）。
