# OKB v2 API 边界：REST 四原语

日期：2026-11-16。基线：配合 [requirements.md](requirements.md)。接口形态：**REST 即可，本期不做 MCP**。消费者：诊断 Agent（主要）、专家最小界面（复用同一 API）。

## 1 查询原语

### 1.1 lookup —— 告警入口

```
GET /api/v1/lookup?alert_signature=<sig>
```

返回：候选异常列表，每项含 anomaly stable_code、display name、confidence、evidence_level、来源计数、revision_id。

### 1.2 trace —— 候选因果链遍历

```
GET /api/v1/trace?anomaly_id=<id>&max_hops=3&top_k=5&facts=<URL编码的观测事实JSON,可选>
```

- `facts`（可选，F3）：调用方已掌握的观测事实（指标、状态、环境）。
- 返回：top-k 候选根因链；每跳带：边 ID、触发条件及求值结果 `passed / failed / unknown`、时间约束、证据等级、出处 ID、revision_id。
- `unknown` 的条件作为"待验证检查项"返回，由调用方取证——这是 evidence-driven 诊断的输入。
- 条件不满足（failed）的边不可走；跨跳组合显式标注"逐跳已核 / 组合推导"。
- 输出有界：max_hops 默认 3，top_k 默认 5，防止调用方上下文饱和。

### 1.3 evidence —— 出处回链

```
GET /api/v1/evidence?id=<node_or_edge_id>&evidence_id=<可选>
```

- 返回：证据列表，每项含 evidence_level、source kind、fragment 回链（工单/文档原文或摘要）。
- 权限（F4）：按 fragment 级 permission_level 校验调用方身份；未授权只返回 summary，不返回原文。

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
- 全部操作服务端识别主体、按动作授权、记审计。

## 3 通用约束

- **所有读响应携带 revision_id**（F5）：调用方答案可复现，钉住修订版本。
- 知识按需查询，不提供"全量导出给 prompt"的接口。
- 错误语义：`404`（实体不存在）、`403`（无权读取原文，仅返回摘要结构时用 `200 + summary_only=true` 标记）、`422`（条件 Schema 不合法）。
- 草稿（draft）知识在 trace/lookup 结果中默认排除，除非显式 `include_draft=true` 且调用方有审核权限。
- 接口契约在本文件冻结；实现切片顺序仍为 S0 建馆 → S1 查询（先建馆后查询）。
