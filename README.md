# InfraUnifiedModel

统一基础设施模型、拓扑同步与可视化，以及独立运维知识管理的单仓库。

| 模块 | 职责与当前状态 |
| --- | --- |
| [UnifiedInfraTopology](UnifiedInfraTopology/README_zh.md) | 已有 Go 拓扑同步与查询代码；MySQL 保存控制面，NebulaGraph 保存当前拓扑。以模块权威模型和实际代码为准，部分 README 是早期快照 |
| [UnifiedInfraWeb](UnifiedInfraWeb/README.md) | 已有 Vue 3 / TypeScript / Element Plus 控制台、拓扑与空间可视化；本轮没有修改运行页面 |
| [UnifiedInfraOKB](UnifiedInfraOKB/README.md) | 新建独立知识模块骨架和交接文档；尚无业务实现、DDL、模型接入或知识页面 |
| UnifiedInfrastructureModel | 既有基础设施模型讨论和资料；不是 OKB 运行服务 |
| topology_grid | 既有 Git 子模块；本轮未初始化，不是 OKB 前置依赖 |

## 运维知识管理从这里接手

先读 [UnifiedInfraOKB 入口](UnifiedInfraOKB/README.md)；向其他 AI 分配工作时使用 [接手提示词](UnifiedInfraOKB/docs/development/ai-handoff-prompt.md)和 [K01—K08 工作包](UnifiedInfraOKB/docs/development/work-packages.md)。

当前 OKB 独立建设资料、多来源 AI 提取和知识组装、专家编辑、审核、版本发布及查询；不关联实际拓扑，不使用 TopologyDetails.vue。前端复用已有应用，后续由明确的前端任务接入，不能将本轮目录视为功能完成。

根协作规则见 [Agents.md](Agents.md)；进入各模块前阅读对应协作约束。仓库文档不授予生产访问、内部资料外传、提交或推送权限。
