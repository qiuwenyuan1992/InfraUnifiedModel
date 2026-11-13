# 房间接入实施计划

用户已确认按房间API接入；UUID稳定身份，房间/机柜/RPP数字引用解析。保留旧cabinet→IDC关系；新增room→IDC、cabinet→room、RPP→room空间located_in。11Tag、4Edge、17合法组合；同房间不是供电连接。

- [ ] 定义room实体与字段、关系矩阵、图Schema及独立新增迁移。
- [ ] 采集room(status=1/idc_id)，完整分页、UUID/inst_id去重；补cabinet.phy_room_id及rpp.idc_room属性，空间解析仅取显式引用。
- [ ] 同步轮次插入room阶段、房间检查点及独立图验收；历史任务不改写。
- [ ] 下钻变为IDC→room→cabinet→device，POD成员不变；上下文含房间，但不从同房间构造供电边。
- [ ] Web类型和阶段标签、目录面包屑跟随更新；三维保留局部机柜视图，不增加楼栋/模组实体。
- [ ] 全量/race/vet/构建，独立Schema审批后执行开发迁移；真实只读验证，是否重跑同步另行明确。

已有未提交修改保留，不提交或清图；房间API说明转写到docs/cmdb/space_room.md但保留用户原证据不修改。
