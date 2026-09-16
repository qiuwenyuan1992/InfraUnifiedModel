# Role & Context
你是一位世界顶级的基础设施架构师与 Nebula Graph 图数据库专家。
目前需要重构美团级别超大规模基础设施拓扑系统。
资产总规模：网络设备 10 万台，物理服务器 50 万台（加上端口、IP等，点边总数达千万级）。
底层资产已实现高成熟度自动化采集，且【每个资源都有全局唯一的 UUID（String 类型）】。
我们需要基于 Nebula Graph 3.x+ (nGQL) 进行高可用、时序化的图数据建模。

# Objective
请帮我设计该基础设施拓扑系统的 **图数据元模型（Meta-Model）**，并输出对应的 Nebula Graph 建表脚本（DDL）与核心查询（nGQL）。

# Design Constraints & Principles
1. VID 策略：统一使用资源的全局唯一 UUID 作为 Nebula Graph 的 Vertex ID（类型为 STRING），避免使用自增 ID 或 IP 以防分布式哈希倾斜。
2. 分层叠加：清晰区分【Layer 1 物理连线与空间拓扑】与【Layer 2 网络逻辑路由拓扑】。
3. 时序版本化（Time-travel）：点和边不能物理删除。所有的边（Edge）必须包含 start_time (int64 timestamp) 和 end_time (int64 timestamp) 属性，通过给 end_time 赋予最大值（如 9223372036854775807）表示当前激活的关系，以此支持查询历史上任意时刻的拓扑状态。
4. 高性能：点和边的属性（Property）要精简，仅保留拓扑关系推导必需的字段。

# Tasks to Execute

## 1. 点（Tag）与边（Edge）的抽象定义
请列出所有核心的“Tag”和“Edge Type”，说明其包含的关键属性（Property）和数据类型（必须包含时序字段）。
至少包含以下实体：
- Tag：device（设备，通过 type 区分交换机/路由器/物理机）、port（接口/网口）、cabinet（机架）、datacenter（数据中心）。
- Edge：connected_to（端口物理连线）、belongs_to（物理空间/宿主归属）、routing_to（网络逻辑路由）。

## 2. Nebula Graph DDL 脚本
请输出符合 Nebula Graph 3.x+ 规范的完整的 nGQL 建图脚本。包含：
- CREATE SPACE（请根据美团规模，合理设置 partition_num=100 并使用 3 副本，VID_TYPE=FIXED_STRING(64)）
- CREATE TAG 语句
- CREATE EDGE 语句

## 3. 基于 nGQL 的高性能查询示例
请使用 Nebula Graph 的 nGQL 语法（推荐使用 MATCH 或 GO），结合时间戳过滤条件（如 `end_time == 9223372036854775807` 代表当前拓扑），编写以下两个场景的查询语句：
- 场景 A：【物理爆炸半径计算】输入某台特定的 Leaf 交换机的 UUID，查询出当前该交换机下连的所有 port，并通过物理连线进一步下钻找出受波及的所有物理服务器（Server）的 UUID 列表。
- 场景 B：【逻辑与物理双活审计】输入两个物理服务器的 UUID，查询它们在【Layer 2 逻辑路由 routing_to】上是否具备多路径冗余，同时下钻审计在【Layer 1 物理连线 connected_to】上是否不幸经过了同一台物理交换机（单点隐患）。

# Input Raw Data Sample (用于参考实体的属性字段)
以下是我们自动化脚本采集到的核心原始数据结构样例，请参考其属性进行建模：
'''
[在此处粘贴或简写你们网络设备 LLDP 数据或服务器网卡数据的 JSON 样本]
'''

在交给 Codex 之前，给您的两个落地避坑建议：Rank 字段的妙用（核心）：Nebula Graph 的边支持 Rank（边权重/边标识）。在处理时序拓扑时，强烈建议建议 Codex 将 start_time 直接作为边的 Rank 值。因为在 Nebula 中，同一个起点和终点如果存在多条边，只能靠 Rank 区分。网络端口如果反复 Up/Down（即物理连线关系断开又重连），用 start_time 做 Rank 才能保证历史数据不被覆盖。超级节点（Dense Node）防御：美团的核心骨干路由器（Spine / Core Switch）可能一个人连了上百台交换机，这就是图数据库里的“超级节点”。在让 Codex 写 nGQL 时，一定要让它建立索引（INDEX），并避免在大促高并发期间使用不带索引的全局 MATCH 扫描。您可以把自动化脚本的 JSON 样例数据直接贴在上述提示词的最后一行发给 Codex。如果您在让 AI 生成代码的过程中遇到了具体的 nGQL 语法报错，或者想让我先帮您预审核一遍 Codex 产出的元模型（Meta-Model），随时可以发给我！