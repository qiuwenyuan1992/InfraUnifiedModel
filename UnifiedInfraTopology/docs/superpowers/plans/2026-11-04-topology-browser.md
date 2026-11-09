# 拓扑浏览实施计划

> 使用test-driven-development执行，独立审查后验收，不提交代码。

Goal: 精确资产定位及一跳展开的真实拓扑浏览。
Architecture: 白名单只读图repository→service→handler→inventory鉴权路由；Vue/Cytoscape增量画布。
Spec: docs/superpowers/specs/2026-11-04-topology-browser-design.md（用户已选择并要求实施）。

验收：全量Go/race/vet/3入口build通过；前端31tests/typecheck/build通过，运行时依赖审计0漏洞。独立审查的构造错误影响HTTP启动、关系矩阵缺校验已补RED→GREEN修复；浏览器定位输入框缺注册补启动回归，同型号GPU标签改为类型+SN。

真实只读Node/Neighbors/HTTP通过：服务器10节点9关系；GPU4节点3关系含2上联；聚合口手动展开后浏览器12节点11关系及ToR，未修改数据。临时端口18000/5176已关闭，用户原8000服务未停止；用户重启后端后新路由生效。无DDL、同步任务、开发报告或提交。

- [x] Task1 图读取repository与SDK值解析：Node/Neighbors查询、白名单、限制/方向/源边界、int64精度和错误值测试RED→GREEN。
- [x] Task2 只读HTTP接口与Wire：授权、ID参数、字段响应、错误404/503、Swagger契约测试RED→GREEN。
- [x] Task3 Web定位/画布/属性面板：真实API、增量合并、并行边/方向保留、布局及预算、过期请求取消，单测与构建。
- [x] Task4 集成及独立审查：全量测试/race/vet/3入口build，前端tests/build；真实只读图库定位并浏览器展示服务器/GPU上联，不修改图数据。

精确身份接口签名在实施中保持一致：repository.TopologyReadRepository.Node(ctx,model.EntityIdentity)(*TopologyNode,error)；Neighbors(ctx,identity,TopologyNeighborQuery)(*TopologyNeighborhood,error)。NewTopologyReadRepository(*viper.Viper)(TopologyReadRepository,func(),error)。Query {Limit int;Direction string;EdgeType model.EdgeType}。Node字段ID SourceID EntityType StableID Label Properties CreatedAt SyncedAt；Edge字段ID SourceID EdgeType RelationKind From To Rank Properties CreatedAt SyncedAt；时间为*time.Time，属性map[string]any，其余JSON字符串；Neighborhood Root *TopologyNode Nodes []TopologyNode Edges []TopologyEdge Truncated bool Limit int。
