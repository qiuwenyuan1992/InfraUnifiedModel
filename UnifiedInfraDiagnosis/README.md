# UnifiedInfraDiagnosis

诊断编排层：LangGraph 状态机执行确定性判定序，消费 OKB REST 四原语完成跨域故障诊断。

- 记忆底座：[UnifiedInfraOKB](../UnifiedInfraOKB)（lookup/trace/evidence/report）
- 判定序依据：[跨域关联全景](../UnifiedInfrastructureModel/跨域关联全景.md)
- 协作约束：[AGENTS.md](AGENTS.md)

## 快速开始

```bash
uv sync                                  # 安装依赖
uv run pytest                            # 回归测试（需 OKB server 在 18090，或用 mock）
uv run python -m diagnosis.cli "T30alertPsuFault"   # 跑一次掉电诊断
```

## 结构

```
diagnosis/
├── tools/
│   ├── okb.py           # OKB REST 客户端（四原语封装）
│   ├── inventory.py     # InventoryPort：拓扑实例查询（fixture 实现，待换 UnifiedInfraTopology）
│   └── llm.py           # LLM 客户端（OpenAI 兼容；可降级，无配置走规则）
├── graphs/
│   ├── unified_graph.py  # 合图：条件路由（1 条告警走正向判定序/N 条走风暴反推，共享 conclude 与飞轮回写）
│   ├── judge_graph.py    # 正向判定序节点工厂（单告警→推演→影响半径→结论）
│   ├── storm_graph.py    # 风暴反推节点工厂（多告警→公共供电祖先→根因）
│   ├── explore.py        # 探索式诊断：LangGraph decide⇄execute 工具回路（LLM tools 协议/规则降级）
│   ├── impact.py         # 影响半径：因果图×拓扑合遍历 + 对账
│   └── storm.py          # 风暴反推算法（纯函数，storm_graph 节点调用）
├── samples.py           # 合成告警数据集（部位+类型+时间→signature 归一）
└── api.py               # FastAPI：SSE 流式诊断 / 风暴 / 对话（LLM）/ 对账 / 证据回链
tests/                    # 判定序回归 + 数据一致性守卫 + 飞轮回写守卫
.env.example              # LLM 网关配置模板（真实 .env 不进 Git）
```

## 待办

- ~~两图合一重构~~（2026-11-24 **已完成**，commit 644eaee）：unified_graph 条件路由合图，explore 一并收编 LangGraph decide⇄execute 回路；前端 SSE 消费统一 composable（useSse.ts）。
- LLM 探索模式 done 帧的 pruned/facts 结构化提取（剪枝结论目前在模型 summary 文本内，规则模式字段完整）。
- checkpointer 会话持久化（F1 纪律声明了未实装；对话上下文目前是进程内存缓存）。
- pending_checks 实体粒度（多告警时 fact_key 分不清是谁的）。
- unmapped 告警的人工登记队列入口（现在只有 error 提示）。
- InventoryPort 真实现：对接 UnifiedInfraTopology REST/Nebula（接口签名已对齐 fixture）。
- 前端 DiagnosisConsole 组件拆分（872 行：场景选择/步骤/结果/风暴/对账/对话/探索可拆子组件，纯代码组织不动行为）。
- 制冷/动环域拓扑实体同步后自动恢复影响半径绑定（知识已预留，等数据）。
