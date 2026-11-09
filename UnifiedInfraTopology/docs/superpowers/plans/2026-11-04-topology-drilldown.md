# 业务下钻实施计划

Goal: 以数据中心/POD目录替代来源选择，设备显示真实多跳依赖。
Spec: docs/superpowers/specs/2026-11-04-topology-drilldown-design.md（用户要求调整）。

实现及集成专项测试通过；Web43tests与build通过。只读真实HTTP验证根目录、POD分页、机柜设备下钻及设备21节点20关系，包括A/B两列头柜、UPS组、UPS成员和变压器，无缺链/截断警告。

审查发现实际query预算被仓储调用次数低估，以及手动筛选误取消自动设备上下文；补RED→GREEN修复，预算在真实execute层扣减，单请求缓存已校验节点且不跨请求。真实浏览器IDC→机柜→设备及POD→直接成员、目录上下页及快捷SN均验证通过。没有选择来源，零/多来源提示配置问题。

未触发CMDB采集、写图、Schema迁移、删除数据或提交；独立临时端口验收完成后关闭，用户8000服务保留。新增路由需用户重启后端。
- [x] Task1 目录repository Roots(ctx,source,entityType,TopologyDirectoryQuery)(*TopologyDirectoryPage,error)、Children(ctx,identity,query)(*page,error)，Query{Limit int;After string}为解签后VID；Page{Items []TopologyNode;NextAfter string}。内部Neighbors Query新增RelationKind model.RelationKind/PeerEntityType model.EntityType用于LIMIT前过滤。测试来源隔离、目录类型、去重/分页RED→GREEN。
- [x] Task2 目录及设备Context service/handler/router，公开Roots/Children返回签名next_after，Context固定遍历及预算，errors/warnings准确，Wire/Swagger更新。
- [x] Task3 Web双入口及面包屑分页、自动单来源、选择设备自动Context，快捷SN保留，Cytoscape/属性面板沿用；过期响应/分支切换/空态测试。
- [x] Task4 全量回归/审查/真实只读HTTP和浏览器验收，文档说明真实路径和边界。
