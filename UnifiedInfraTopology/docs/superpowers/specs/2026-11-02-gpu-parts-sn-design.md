# GPU 实际 SN 身份设计

**状态：** 用户已于 2026-11-02 上一轮明确批准；直接实施，不再重新评审。

## 已批准契约

当前用户批准的同步流程：一次完整采集（含分页/目录查询），身份/全局冲突/端点校验→写图→独立查图库验收。写图后不再次读取 CMDB，不以第二遍来源差异产生 `source_changed`；独立只读调试测试可比较两次来源调用，但不属于生产同步流程。

- `server_gpu.parts_sn` 是实际 GPU SN，也是唯一稳定身份；完整逻辑身份为 `source_id + gpu + parts_sn`。仅 trim 首尾空白，不改变大小写。
- 缺失、空字符串或纯空白 `parts_sn` 的卡记录直接忽略，不创建 GPU 或关系；不使用 `uuid`、`id`、`inst_id`、槽位或型号回退。
- CMDB 卡记录 `server_gpu.uuid` 不参与身份，也不保存到 GPU 图节点；没有兼容字段、双写或旧身份映射。
- 2026-11-06 查询契约更正：先以服务器 `condition.device_sn` 完整分页查询 `server_gpu`，完成有效卡及全量冲突识别后，再对有有效卡的服务器使用 `condition.device_sn` 完整分页查询 `gpu_uplink`，不逐卡请求。返回的 `gpu_sn` 仅用于解析同一服务器所属的有效 `parts_sn`；缺失、未知 SN 或归属/属性冲突的记录跳过，没有有效卡的服务器不请求上联。
- `contains_gpu` 两端身份为服务器 SN 与 GPU `parts_sn`；`gpu_uplink.uuid` 仍是来源关系标识，关系 ID 和 Rank 规则不变。ToR 接口仍由 `tor_sn + tor_port` 解析到 `port_uuid`。

## Schema 与执行边界

- 基线 GPU Tag 删除 `uuid`，将 `parts_sn` 定义为 `STRING NOT NULL`；身份索引为 `source_id(64), parts_sn(192)`。
- 现有 GPU 节点数为 0；已有 Schema 不会因 `CREATE TAG IF NOT EXISTS` 自动改变，必须另交 `0004_gpu_parts_sn_identity.ngql`，显式删除旧索引、ALTER Tag、重建 SN 索引。
- NebulaGraph `CHANGE ... NOT NULL` 要求 `DEFAULT`；增量 DDL 中的空字符串默认值仅满足 Schema 语法，不能被采集或写入代码当作可用 SN。写入始终显式提供非空 `parts_sn`；执行前必须再次确认 GPU 节点数为 0。
- 本任务只编写并离线验证 DDL，不连接数据库、不执行 live DDL、不提交代码；独立迁移只由人工审批后执行。

## 验收

历史验收记录：Schema 测试先修改并运行失败，再修改基线和迁移并运行成功；文档中的 GPU 卡 UUID 身份已替换。2026-11-06 更正后，上联查询遵循上述按服务器 `condition.device_sn` 完整分页的契约，不再将按服务器请求视为旧契约；本次仅更正文档，未重新执行历史测试或 live 验证。适配器、服务、repository 与 CLI 由父任务实施，不属于本子任务文件范围。
