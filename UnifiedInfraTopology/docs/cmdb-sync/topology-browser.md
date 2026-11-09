# 拓扑浏览第一期

在平级 `UnifiedInfraWeb` 的“拓扑浏览”中直接选择“数据中心”或“POD”目录，不再选择来源。后台恰好一个启用CMDB来源时自动使用；零个或多个时提示配置问题，不任意选择。

- 数据中心→机柜→设备：按实际空间边下钻，目录有分页和面包屑。
- POD→成员设备：按device→pod/member_of去重列举，不能通过机柜POD标签推导成员。
- 选择设备后自动展示机柜、A/B列头柜、UPS组/UPS成员、变压器、GPU与已同步网络上联；供电沿真实机柜路径，UPS为旁支成员，不生成服务器直连电力边。
- 设备SN快捷定位保留；其他节点的手动一跳与属性查看也保留。不执行CMDB采集，不写图，没有名称模糊搜索或全图库加载。

| 类型 | 输入的稳定身份 |
| --- | --- |
| device | 设备SN，例如 `A514359X5413481` |
| gpu | GPU实际parts_sn，例如 `1750125024470` |
| interface | CMDB port_uuid，不是端口名称 |
| cabinet / data_center / pod | CMDB uuid |
| transformer / ups_group / ups / rpp | 正整数inst_id |

## 只读API

复用同步管理鉴权开关、JWT读取权限及响应封装；免鉴权只适用于受控开发环境。

```text
GET /v1/inventory/topology/node
  ?source_id=<32位来源ID>&entity_type=device&stable_id=<SN>

GET /v1/inventory/topology/neighbors
  ?source_id=<来源ID>&entity_type=device&stable_id=<SN>
  &direction=both&limit=100&edge_type=composition_relation
```

direction支持in/out/both，默认both；limit默认100、最大200；edge_type可省略或指定spatial_relation、composition_relation、network_relation、power_relation之一。未知、重复或非法参数拒绝，不接受任意nGQL。

详情返回业务身份、VID、当前属性及创建/同步时间。邻居返回根节点、节点数组、边数组、数量限制及truncated标记。rank返回字符串，避免浏览器丢失64位整数精度；平行边按relation_id区分，不合并A/B路或不同plane。

## 目录与设备关联API

```text
GET /v1/inventory/topology/roots?source_id=<来源ID>&entity_type=data_center&limit=50
GET /v1/inventory/topology/roots?source_id=<来源ID>&entity_type=pod&limit=50
GET /v1/inventory/topology/children?source_id=<来源ID>&entity_type=data_center&stable_id=<IDC_UUID>&limit=50
GET /v1/inventory/topology/device-context?source_id=<来源ID>&stable_id=<设备SN>
```

目录children只接受data_center、cabinet、pod父类型；响应items、next_after，下一页传 `after=<next_after>`。分页单位是子节点，POD双plane关系不产生重复设备项。游标签名绑定用户、来源、父身份和类型，不能跨目录复用；limit默认50最大100。

设备上下文固定追踪关系，不进行通用递归；每步50边、200节点/400边，最多40次实际图库请求。请求内复用已验证节点，预算耗尽保留完成路径并返回truncated/warnings；不读取同柜其他设备、同UPS组其他RPP或端口上其他服务器/GPU。A/B双边保留，UPS成员不是串联层。数据缺关系或采集未覆盖时明确提示，不能据此认定现实没有供电。

## 页面说明

- 中心画布支持缩放、拖拽、居中及布局；选择节点查看属性，手动展开其邻居。
- 可选择展开方向与关系领域；返回截断时明确提示，不宣称已展示全部拓扑。
- 单次最多200边；画布最多500节点/1000边，超过预算时保留原图并提示缩小范围。
- 网络、空间、组成、电力用不同颜色，箭头遵循图存储方向。物理links_to为互联语义，存储排序方向不代表流量；备用关系不等于实际供电切换。
- 不从空上联补造节点，不把BAD_TYPE解释为正常资产属性。资产不存在返回404，图库不可用返回503。
- 当前读取的是同步图状态，不证明跨接口原子快照；旧边清理尚未实施，历史关系可能残留，需结合synced_at判断。

## 开发验收

默认测试使用替身验证查询、参数、身份与图合并，不依赖外部服务。真实只读图库测试须显式设置 `TOPOLOGY_BROWSER_READ_CONFIG`；不执行清理或写入。完整Go测试、相关包race、vet及三个入口构建通过；前端31个测试、TypeScript及生产构建通过，运行时依赖安全审计0漏洞。

真实只读测试：设备 `A514359X5413481` 返回10节点/9关系，GPU `1750125024470` 返回4节点/3关系（2条GPU上联）；HTTP接口、limit=1截断及不存在资产404验证通过。浏览器手动展开聚合口后展示12节点/11关系及所属ToR，无错误、无写图。

独立审查发现的初始化连接失败拖垮HTTP、非法关系组合、未注册输入框问题已补RED→GREEN回归并修复。连接/配置失败保持不可用只读repository，API返回503；修复配置后重启服务。关系矩阵复用模型定义，不依赖缺失的GPU来源UUID重新生成ID。

**现有8000服务必须重启才能加载新路由**，然后刷新Web进入“拓扑浏览”。本次独立临时验收服务已关闭，未停止用户服务，无Schema迁移、同步任务、旧边清理或开发报告文件。
