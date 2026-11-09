# 拓扑浏览第一期

用户选择资产中心式第一期并要求实现查看效果。建立只读API与Vue图画布，定位资产后手动展开一跳；不加载整IDC、无故障模拟、不扩大采集范围、不增改图库Schema。

## 方案与边界

采用资产中心式而非空间总览：当前约4万节点，完整布局不可读；按稳定身份定位真实资产，然后展开空间、组成、网络、电力关系。第一期不承诺名称模糊搜索，无名称索引时不能把截断候选当完整搜索。

用户输入来源、实体类型和稳定ID：device=device_sn、gpu=parts_sn、interface=port_uuid、柜/IDC/POD=uuid、电力=正整数inst_id。节点详情包含原始领域属性及同步时间；图按实际relation_id去重，保留原方向、A/B路、plane、rank（字符串），links_to仅显示存储方向并标注物理互联。无可选MATCH属性读取，不返回BAD_TYPE或把错误吞成NULL。

页面增加“拓扑浏览”导航：顶部定位表单，中间可拖拽缩放画布，右侧节点/关系属性；点节点选择，按钮展开邻居；关系领域可选，显示节点/边数量及截断提醒。使用Cytoscape，增量合并保留已显示节点位置。默认每次最多100边，后端最多200；画布最多500节点/1000边，拒绝超预算而不丢失已有图。切换根节点丢弃旧请求，展示加载/空/错误状态。只读不自动创建任务。

## API契约

复用 `/v1/inventory` 的鉴权开关及同步读取权限，无新登录页。只读GET：
- `/topology/node?source_id=...&entity_type=...&stable_id=...`：返回节点对象；不存在404。
- `/topology/neighbors?source_id=...&entity_type=...&stable_id=...&limit=100&direction=both&edge_type=...`：返回 `{root,nodes,edges,truncated,limit}`。direction为in/out/both，edge_type可为空或四类之一。

节点 `{id,source_id,entity_type,stable_id,label,properties,created_at,synced_at}`；边 `{id,source_id,edge_type,relation_kind,from,to,rank,properties,created_at,synced_at}`。属性保留正确字符串/数值/布尔/列表/普通null，特殊错误值拒绝；非安全JS整数以十进制字符串返回。

白名单Tag/Edge，用户文本参数化，限制正整数范围，不提供任意nGQL。详情用确定性VIDFETCH，邻居查询双向不改实际src/dst；只取同source有效端点，limit+1判截断。返回规模限制不等同底层扫描成本限制；配置/图库不可用503，不在HTTP创建连接风暴。图时间是同步标记，不承诺原子全局快照或旧边收敛。

## 实现与验证

只读repository与service/handler/Wire独立于worker，长寿命图连接通过Wire清理。前端独立topology模块，不改已有任务行为。单测覆盖身份、查询白名单、双向与平行边、特殊值/空点、truncate、鉴权/参数；前端覆盖图合并/预算、过期响应和接口错误。全量Go/race/vet/build、Vue测试/build、真实图库只读和浏览器验收。无自动提交或数据库DDL。
