# 拓扑浏览业务下钻调整

用户确认调整为数据中心或POD层层下钻，隐藏技术来源选择；设备视图自动展示供电、GPU和网络依赖。沿用既有只读API/鉴权和图模型，不改来源采集范围、不造device→电力边。

默认恰好一个启用CMDB来源时自动解析；零个或多个则明确提示需后台配置，不任意选来源。页面入口为数据中心列表和POD列表；数据中心→机柜→设备，POD→成员设备（按VID去重，不通过柜标签推导）。目录按VID有界分页，返回下一页after标记绑定来源及父身份；不一次拉全图库。服务器SN作为快捷定位保留。

选中设备固定追踪路径：device→cabinet→RPP(A/B)→UPSGroup→transformer，并查UPSGroup的UPS成员旁支；device→GPU→上联接口←所属ToR，device→服务器上联接口←ToR。只按明确关系类型、对端类型及方向读取，不扩散整柜设备、其他RPP或端口上的其他服务器/GPU。transformer/UPS/远端ToR为叶子，不自动追备用循环。真实图节点和边按ID合并，A/B/plane保留。缺少关系显示不完整，不能从属性合成边。

接口为GET /inventory/topology/roots（source_id,entity_type=data_center|pod,limit,after可选）、/children（父完整identity,limit,after可选）、/device-context（source_id,stable_id）。目录固定类型矩阵，不提供任意图查询；limit默认50最大100，after为64位VID，响应items,next_after；前端页状态分离，后端签名游标将after包装并绑定父/source/type（接口after为不透明签名字符串）。

一跳内部增加RelationKind、PeerEntityType条件必须在LIMIT之前过滤。设备context固定每步50边、最多40查询/200节点/400边，返回root,nodes,edges,truncated,limit及warnings字符串数组。预算或某步截断明确提示；已完成路径可以保留，故障则返回明确错误。图详情和手动展开保留，输入/路径切换取消过期响应。

先RED→GREEN，覆盖POD双plane去重分页、父游标隔离、A/B双边、UPS旁支和端口归属不扩散、设备缺柜、单来源及切目录过期响应。全仓Go/race/vet/build、Webtests/build，真实目录及设备供电只读/浏览器验收。无写图、DDL、提交或开发报告。
