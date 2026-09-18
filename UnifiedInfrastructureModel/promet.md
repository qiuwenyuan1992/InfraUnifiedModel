# Role & Context
你是一位世界顶级的基础设施架构师与图数据库（Graph Database）专家。
目前需要重构美团级别超大规模基础设施拓扑系统。
资产总规模：网络设备 10 万台，物理服务器 50 万台。底层数据已实现高成熟度自动化采集。
由于规模巨大，传统关系型数据库的多度关联查询（爆炸半径计算、根因分析）遇到严重性能瓶颈。我们决定引入分布式图数据库（如 Nebula Graph / Neo4j）进行重构。

# Objective
请帮我设计该基础设施拓扑系统的 **图数据元模型（Meta-Model）**，并输出对应的建模定义与查询逻辑。

# Design Constraints & Principles
1. 分层叠加：清晰区分【Layer 1 物理连线与空间拓扑】与【Layer 2 网络逻辑路由拓扑】。
2. 时序版本化（Time-travel）：点和边不能直接物理删除，必须支持通过时间戳（如 start_time, end_time）查询历史上任意时刻的拓扑状态。
3. 高性能：点和边的属性（Property）要精简，仅保留关系推导必需的字段（如端口名称、光功率、BGP状态、VLAN ID）。

# Tasks to Execute
请分步骤输出以下内容：

## 1. 点（Vertex / Tag）与边（Edge）的抽象定义
请列出所有核心的“点”和“边”，并说明其包含的关键属性（Property）和数据类型（注意：必须包含时序字段）。
例如：
- 点：Datacenter, Room, Cabinet, Device(Switch/Router/Server), Port, IP...
- 边：CONNECTED_TO（端口到端口）, BELONGS_TO（物理空间归属）, ROUTING_TO（BGP/OSPF逻辑路由）...

## 2. 图数据库 Schema DDL 脚本
请选择 [Nebula Graph 或 Neo4j Cypher] 语法，输出完整的建图（Create Tag / Edge / Schema） DDL 脚本。

## 3. 基础设施组核心场景的查询语句示例（Cypher / nGQL）
请针对以下两个基础设施组最核心的刚需场景，编写高性能的图查询示例语句：
- 场景 A：【物理爆炸半径计算】输入某台特定的 Leaf 交换机 ID，查询出由于其单点故障，在物理和空间拓扑上会波及的所有机架（Cabinet）以及受影响的物理服务器（Server）列表。
- 场景 B：【逻辑与物理双活审计】输入两个物理服务器 IP，查询它们在【Layer 2 逻辑路由】上是否具备多路径冗余，同时下钻审计在【Layer 1 物理连线】上是否不幸经过了同一台物理交换机（单点隐患）。

# Input Raw Data Sample (用于参考实体的属性字段)
以下是我们自动化脚本采集到的核心原始数据结构（或者是 CMDB 字段快照），请参考其属性进行建模：
'''
[在此处粘贴或简写你们网络设备 LLDP 数据或服务器网卡数据的 JSON 样本]
'''