# UnifiedInfraOKB 接口与模块边界

日期：2026-11-16。状态：接口设计轮廓，尚无已实现 API 或 OpenAPI。K01 将本文和需求基线落实为统一机器契约；其他任务不得据此自行创造不同字段。

## 1 对外边界

UnifiedInfraOKB 是独立服务，命名空间建议 /v1/knowledge。前端由同源代理分发到该服务；现有 /v1/inventory 继续属于 UnifiedInfraTopology。本期不存在资产或拓扑匹配接口。

| 资源组 | 接口职责 | 关键约束 |
| --- | --- | --- |
| documents / versions | 登记资料、上传或导入版本、查询处理结果 | 不可变原件；URL 导入策略、格式和预算在 K01 冻结 |
| parses / spans | 发起解析、读取原文定位、查看覆盖及失败 | 绑定原件和解析版本；不虚构页码；大文件通过受控下载 |
| topics / gaps | 主题问题、资料集合、缺口和补查记录 | 支撑、冲突、未知、不适用分开；不是拓扑域 |
| extractions / attempts | 发起异步提取、查询、取消和重试 | 202 + task ID；输入 manifest；幂等和旧尝试隔离 |
| candidates | 原子陈述或知识草稿候选、差异、接纳/拆合/驳回 | 接纳不等于批准；原子陈述不直接变成第四类正式知识 |
| items / revisions | 三类知识 CRUD、固定修订和来源/依赖 | 草稿并发版本；提交审核后冻结；不读浮动 latest |
| reviews | 提交审核和记录决定 | 服务端主体；revision/hash/依赖上下文绑定 |
| releases / activations | 构建、启用、回退和撤回 | 固定依赖闭包；文件就绪后原子切换指针 |
| search | 资料和正式知识的独立查询 | 文档版本和 release_id 过滤，返回出处；草稿明确标识 |

路径、DTO、错误码、预算和分页方式由 K01 定为单一契约。未来生产客户端由契约约束，不把 GORM 实体或模型原始响应直接暴露为 API。

术语对齐：span/fragment 都指固定解析版本中的原文定位片段，存储设计建议用 source_fragment / fragment_id；parses 对应 parse_version。早期讨论中的 source_spans、document_parses 等只是概念称呼，不要求建立平行对象。K01 字段字典统一最终 API 和存储映射，后续文档不得再各用一套含义。

## 2 契约中必须定义的公共元素

- API 不透明字符串 ID；不借用拓扑身份或数字资产 ID。
- 业务时间使用 UTC、微秒精度；前端按用户时区显示，文档日期不替代事件时间。
- request_id、分页/过滤、幂等、并发版本、错误分类及重试语义。
- source document/version/parse/span、job/attempt、candidate、knowledge revision、review、release 的独立引用。
- 资料与知识的有效、缺失、未支持、失败、未知及撤回状态；空数组不能兼任所有状态。
- 错误字段路径、缺口/冲突明细、来源定位和内容摘要。
- 文件上传/下载与 JSON API 的区分，格式、字节预算、URI 引用和文件就绪状态。
- 真实主体和动作权限；调用方不能通过传入用户名自行获得审核/发布能力。

## 3 状态机建议

以下拼写供 K01 统一冻结，当前不是运行时枚举。

| 对象 | 状态与说明 |
| --- | --- |
| extraction job | queued、running、succeeded、failed、canceled；尝试另记代次与失败原因 |
| candidate | pending、accepted、rejected；有处理事件及拆合关联，accepted不表示已发布 |
| revision | draft、in_review、approved、rejected；published属于发布包关系，非可变正文状态 |
| release | building、published、failed、withdrawn；published不等于当前启用，active指针独立 |
| topic coverage | 由目标问题的缺口矩阵表示，不能只用完成百分比或模型自评分 |

资料解析状态需定义 unsupported 和失败/待处理区分。知识来源核验与可访问性是独立记录，不因 URL 暂时不可达就自动否定内容。

撤回为不可重新启用的终态：withdrawn 包修正后构建新包。回退只选择仍为 published 且通过当前来源/依赖检查的历史包。

## 4 组件消费接口

以下为职责级接口，不是已存在的 Go signature。K01/K02 把输入输出落实到同一契约和包，后续任务消费固定版本。

| 组件 | 输入 | 输出 |
| --- | --- | --- |
| DocumentStore | 字节流/大小/期望摘要及读取主体 | 不可变对象引用、实际摘要、就绪或失败状态 |
| Parser | 固定资料版本、解析配置与预算 | parse ID、定位片段、覆盖清单、unsupported/失败 |
| IdentityVerifier / Authorizer | 服务端凭证、动作及目标 | 真实 subject、允许或明确拒绝 |
| ExtractionProvider | 固定片段及上下文 manifest、Schema、模型配置、尝试代次 | 原始输出引用、结构化候选或校验失败，不直接写正式知识 |
| KnowledgeService | 候选/专家输入、指定修订及并发条件 | 草稿或冻结修订、来源与依赖校验结果 |
| ReleaseBuilder | 指定批准修订和依赖、原文及评测引用 | 不可变包、manifest、摘要、构建结果 |
| SearchReader | 指定资料版本或指定发布包、过滤条件 | 确定版本的知识或片段、引用及限制 |

provider 在生产接获准模型，在测试接固定响应；两个模式都需保留输入版本和执行记录。模型 SDK、解析器和身份策略的具体选择属于任务明确权限，敏感配置不进入交付文件。

## 5 前端适配

新增独立 knowledge API client。现有 inventoryRequest 固定 /v1/inventory 和 JSON Content-Type，不直接用于文件上传。复用通用响应和 request ID 思路，上传使用正确媒体类型，错误文案区分知识和同步业务。

知识页面不依赖 sourcesLoaded 或 CMDB 错误状态。由 K07 按契约处理 loading、失败、空结果、取消、未支持、冲突、未保存草稿与刷新恢复。UI 隐藏按钮不能代替后端授权。

## 6 冻结顺序

K01 冻结 Schema、DTO、状态与合成验收包；K02 落实依赖装配、MySQL 与文件存储/身份适配接口；K03/K05 分别实现资料与知识；K04 接入主题和提取；K06 发布查询；K07 UI；K08 真联调。详见 [工作包](../development/work-packages.md)和[实施计划](../development/plans/2026-11-16-okb-implementation.md)。

[返回需求基线](requirements.md)
