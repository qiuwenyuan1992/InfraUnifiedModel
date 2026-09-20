# GPU 实体定义

状态：最小拓扑模型设计稿。依据截至 2026-09-20 已确认的 CMDB 规则和样例整理；用于 GPU 资产、服务器包含关系和 GPU 上联分析，不复制完整 `server_gpu` 或 `gpu_uplink` 记录。

## 1. 设计目标

`gpu` 表示 GPU 服务器中的独立 GPU 卡实例。GPU 卡来源于 `server_gpu`，上联关系来源于 `gpu_uplink`。

本实体遵守以下边界：

1. 先从 `device_view` 识别 GPU 服务器，再按服务器 `device_sn` 查询该设备包含的 GPU 卡。
2. GPU 卡节点按当前模型约定以 `server_gpu.uuid` 作为稳定身份；`parts_sn` 是来源匹配键和资产属性，不替代 UUID。
3. GPU 上联通过 `gpu_uplink.gpu_sn = server_gpu.parts_sn` 解析 GPU，再通过 `tor_sn + tor_port` 解析网络设备接口。
4. `ib1`、`ib8` 等 `gpu_port` 按已确认语义表示 GPU 关联网卡端口，当前不建立网卡或网卡端口节点。
5. NebulaGraph 只保存 GPU 节点和已解析成功的关系；MySQL 不保存 GPU 快照、上联候选或临时别名索引。
6. 查询条件严格使用接口已明确支持的字段：`server_gpu` 使用 `device_sn` 或 `parts_sn`，`gpu_uplink` 使用 `device_sn` 或 `gpu_sn`；不假定 API 支持按 UUID 查询。

## 2. 采集顺序

### 2.1 查询 GPU 卡

从 `device_view` 中识别 GPU 服务器：

```text
parent_type_id = 52
device_type_id = 58
```

对每台 GPU 服务器，以最终设备身份 `device_sn` 查询 `server_gpu`：

```json
{
  "condition": {
    "device_sn": "219685020073"
  }
}
```

完整分页读取该服务器的 GPU 卡后：

1. 按 `server_gpu.uuid` 创建或更新 GPU 节点。
2. 通过 `server_gpu.device_sn = device.device_sn` 建立 `contains_gpu` 关系。
3. 不使用设备汇总字段 `parts_count.gpu` 或 `parts_gpu` 代替 GPU 卡明细。

### 2.2 查询 GPU 上联

GPU 卡读取完成后，以同一服务器 `device_sn` 查询 `gpu_uplink`：

```json
{
  "condition": {
    "device_sn": "219685020073"
  }
}
```

API 也支持按单张卡的 `gpu_sn` 查询，但完整同步优先按服务器查询全部上联，避免逐卡请求：

```json
{
  "condition": {
    "gpu_sn": "MY15960263P00569"
  }
}
```

上联记录按以下字段解析 GPU：

```text
gpu_uplink.device_sn = server_gpu.device_sn
gpu_uplink.gpu_sn    = server_gpu.parts_sn
```

解析成功后，再以：

```text
gpu_uplink.tor_sn   = port_view.local_device_sn
gpu_uplink.tor_port = port_view.port_name
```

查询或匹配唯一的 `port_uuid`，建立 `gpu_uplink` 关系。端口解析细节以 [interface.md](interface.md) 为准。

## 3. GPU 图节点

### 3.1 身份

GPU 最终身份使用：

```text
server_gpu.uuid
```

逻辑 GPU 身份：

```text
scope_id:source_id:gpu:uuid
```

以下字段均不能替代 GPU UUID：

- `server_gpu.id`
- `server_gpu.inst_id`
- `parts_sn`
- `index`
- `slot`
- `bus_address`

`parts_sn` 用于将 `gpu_uplink.gpu_sn` 解析到 GPU UUID。即使当前样例中配件 SN 唯一，也不将其提升为最终身份。

### 3.2 当前保存字段

| 目标字段 | 来源 | 必要性 |
|---|---|---|
| `scope_id` | 同步上下文 | 隔离租户或拓扑范围 |
| `source_id` | 同步配置 | 避免不同 CMDB 来源之间发生身份碰撞 |
| `uuid` | `server_gpu.uuid` | GPU 卡稳定身份 |
| `parts_sn` | `server_gpu.parts_sn` | GPU 资产 SN，也是上联匹配键 |
| `parts_number` | `server_gpu.parts_number` | GPU 配件编号 |
| `manufacturer` | `server_gpu.manufacturer` | GPU 厂商 |
| `model` | `server_gpu.model` | GPU 型号 |
| `specification` | `server_gpu.specification` | GPU 规格描述 |
| `card_index` | `server_gpu.index` | GPU 在服务器中的来源序号；不参与身份判断 |
| `slot` | `server_gpu.slot` | GPU 来源槽位 |
| `bmc_slot` | `server_gpu.bmc_slot` | BMC 槽位，允许为空 |
| `bus_address` | `server_gpu.bus_address` | PCI 总线地址 |
| `connector` | `server_gpu.connector` | 连接器类型 |
| `conn_ports[]` | `server_gpu.conn_ports` | GPU 关联网卡端口名称，用于上联核对 |
| `vram` | `server_gpu.VRAM` | 显存规格；保持来源字符串，不自行换算单位 |
| `tdp` | `server_gpu.tdp` | 热设计功耗；保持来源字符串，不自行推断单位 |
| `computer_perf` | `server_gpu.computer_perf` | 来源计算性能描述 |
| `gpu_fp` | `server_gpu.gpu_fp` | 来源浮点性能描述 |
| `gpu_fp64` | `server_gpu.gpu_fp64` | 来源 FP64 性能描述 |
| `fp16_tensor_sparsity` | `server_gpu.FP16_Tensor_Sparsity` | 来源性能值，允许为空 |
| `fp4` | `server_gpu.FP4` | 来源性能值，允许为空 |
| `int4` | `server_gpu.INT4` | 来源性能值，允许为空 |
| `int8` | `server_gpu.INT8` | 来源性能值，允许为空 |
| `tf16` | `server_gpu.TF16` | 来源性能值，允许为空 |
| `tf32` | `server_gpu.TF32` | 来源性能值，允许为空 |
| `driver_ver` | `server_gpu.driver_ver` | 驱动版本 |
| `firmware_ver` | `server_gpu.firmware_ver` | 固件版本 |
| `npu_versions` | `server_gpu.npu_versions` | 来源版本信息；当前保持原始字符串 |
| `is_domestic` | `server_gpu.is_domestic` | 国产化标识；按来源值保存 |
| `maintenance_status` | `server_gpu.maintenance_status` | 维保状态原值 |
| `maintenance_end` | `server_gpu.maintenance_end` | 维保结束日期 |
| `created_at` | 同步任务 | 本项目首次入图时间，只在首次创建时写入 |
| `synced_at` | 同步任务 | 本轮完整同步最后见到时间，用于安全清理旧节点 |

字段规范：

- 空字符串统一写为 `NULL`；空数组写为空数组，不写含空字符串的数组元素。
- `index` 来源为字符串，目标字段命名为 `card_index`，避免与查询关键字混淆；保持来源值，不据此推导卡身份。
- `VRAM`、`tdp` 以及性能字段的单位未形成统一结构化契约，当前保存原始字符串。
- `npu_versions` 当前是 JSON 字符串；因尚无正式结构契约，暂不拆分为独立属性。

### 3.3 当前不保存字段

以下字段不进入 GPU 节点：

- `server_gpu.id`
- `device_sn`：只用于查询所属服务器和生成 `contains_gpu`，正式归属由关系表达
- `inst_id`
- `obj_id`
- `create_time`
- `last_time`
- `creator`
- `modifier`
- `data_from`
- `manufacturer_id`
- `model_id`
- `specification_id`
- `idc_name`
- `maintenance_status` 之外的资产管理流程字段
- `supplier_account`
- `plat`
- `silkscreen`
- `name`：当前样例为空，GPU 展示名由 `model + parts_sn` 组合生成，不单独保存空名称

`gpu_uplink` 中的设备、GPU、POD 和 ToR 字段只作为关系解析或校验输入，不复制到 GPU 节点。

### 3.4 GPU 地址

`server_gpu` 当前没有卡级 IP 字段，已提供的 8 条 `gpu_uplink.gpu_ip` 均为空字符串。因此当前 GPU 节点不定义 `ip_addresses[]`。

处理规则：

1. 空 `gpu_ip` 不保存。
2. `gpu_ip` 可作为 `gpu_uplink` 的可选来源关系属性保留。
3. 取得非空样例并确认它确实表示 GPU 卡级地址后，再评估是否归并到 GPU 节点。
4. `device_ip` 是服务器地址，不能写入 GPU 节点。
5. `tor_ip` 是上联记录中的 ToR 地址来源字段，但当前多数值为回环地址；不能写入 GPU 节点，也不能用于身份解析。

## 4. 关系解析与校验

### 4.1 服务器包含 GPU

使用：

```text
server_gpu.device_sn → device.device_sn
server_gpu.uuid      → gpu.uuid
```

生成：

```text
device(device_sn) -[contains_gpu]-> gpu(uuid)
```

规则：

- `device_sn` 为空或设备不存在时，不创建孤立 GPU 节点和关系，记录同步诊断。
- 所属设备应为 GPU 服务器；当前以 `device_type_id=58` 校验。
- `parts_count.gpu` 只可用于数量核对，不可代替实际 GPU 节点数量。
- 同一服务器下重复的 GPU UUID 只创建一个节点。

### 4.2 GPU 上联匹配

先匹配 GPU：

```text
(gpu_uplink.device_sn, gpu_uplink.gpu_sn)
    → (server_gpu.device_sn, server_gpu.parts_sn)
    → gpu.uuid
```

再执行以下交叉校验：

| `gpu_uplink` | `server_gpu` | 规则 |
|---|---|---|
| `gpu_port` | `conn_ports[]` | 非空时应属于该 GPU 的关联网卡端口列表 |
| `gpu_slot` | `slot` | 两端非空时应一致 |
| `gpu_model` | `model` | 两端非空时用于辅助校验，不参与身份判断 |
| `gpu_vram` | `VRAM` | 当前样例为 `80GB` 与 `80G`，格式不一致；在单位契约明确前仅记录来源值，不作为冲突判定 |

关键身份匹配失败时不生成关系。`gpu_port` 或 `gpu_slot` 不一致时记录 `conflict`；`gpu_model` 仅记录辅助差异。不得改用槽位、端口名、型号或显存猜测 GPU 身份。

### 4.3 解析 ToR 接口

使用：

```text
(gpu_uplink.tor_sn, gpu_uplink.tor_port)
    → (port_view.local_device_sn, port_view.port_name)
    → interface.port_uuid
```

规则：

- 必须限定在 `tor_sn` 对应设备范围内匹配 `tor_port`，不能仅按端口名跨设备匹配。
- `tor_sn` 或 `tor_port` 为空、目标接口不存在或匹配不唯一时，不生成上联边并记录同步诊断。
- `tor_name`、`tor_role` 可用于展示或辅助核对，但不能替代 `tor_sn`。
- `tor_ip` 当前样例多数为 `127.0.0.x`，按低质量来源值处理，不用于接口身份、查询或一致性校验。
- 当前样例已闭环 GPU 端；若对应 ToR 接口尚未采集，GPU 节点和 `contains_gpu` 可以写入或刷新，但该服务器的上联范围不得标记为完整成功，不伪造接口或上联边，也不得清理仍可能有效的旧上联边。

解析成功后生成：

```text
gpu(uuid) -[gpu_uplink]-> interface(port_uuid)
```

## 5. 拓扑关系

| 关系 | 端点 | 通用属性 | 业务属性 | 来源 |
|---|---|---|---|---|
| `contains_gpu` | `device(device_sn) → gpu(uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | 无 | `server_gpu.device_sn + uuid` |
| `gpu_uplink` | `gpu(uuid) → interface(port_uuid)` | `relation_id`、`scope_id`、`source_id`、`created_at`、`synced_at` | `tor_port` 是建边所需来源字段；边属性均允许为空：`gpu_port`、`gpu_port_speed`、`gpu_ip`、`gpu_slot`、`server_port_speed`、`bond_name`、`tor_port`、`tor_port_speed`、`tor_role`、`source` | `gpu_uplink` |

上联速率字段全部保持来源字符串，不进行单位换算或跨字段数值比较；当前样例的 `gpu_port_speed/server_port_speed` 使用 `400G/100G`，而 `tor_port_speed` 使用 `400000`，其单位尚未由接口契约明确。

关系身份规则：

- `gpu_uplink.uuid` 是来源关系身份；图中的 `relation_id` 使用 `scope_id:source_id:gpu_uplink:uuid`，避免不同范围或来源发生碰撞。
- `contains_gpu` 没有独立来源关系 UUID，由关系类型和两端完整逻辑身份确定性生成；两端身份必须包含 `scope_id`、`source_id`、对象类型和稳定身份。
- 同一 GPU 连接同一交换机但接口不同，必须保留为不同上联关系。
- 上联边的终点是网络设备 `interface(port_uuid)`，不是网络 `device` 节点。
- `gpu_port` 只是关系属性，不创建 GPU 侧接口节点。

## 6. 同步约定

1. 只对已识别的 GPU 服务器查询 `server_gpu`；按服务器完整分页读取 GPU 卡。
2. GPU 卡采集完成后，再按同一 `device_sn` 完整分页查询 `gpu_uplink`。
3. GPU 节点按 `server_gpu.uuid` 去重；上联记录按 `gpu_uplink.uuid` 去重。
4. 在单台服务器请求范围内，可使用本次 `server_gpu` 响应构建临时 `parts_sn → GPU UUID` 映射，也可按 `parts_sn` 再查 `server_gpu`；该映射不跨服务器、不保存在 MySQL，也不成为整轮同步状态。
5. ToR 接口按 `tor_sn + tor_port` 查询或匹配 `port_view`；不建立整轮 `tor_sn → device` 或 `(tor_sn, tor_port) → port_uuid` 索引。
6. API 请求处理中的局部匹配结果不作为持久化状态；重试、恢复和图重建均重新读取 CMDB。
7. “发布成功”只表示该轮完整采集、解析和图写入成功后将任务状态置为成功；当前不承诺图写入过程中的原子快照可见性。
8. 本轮所有 GPU 节点和成功解析的关系统一刷新 `synced_at=T`；`created_at` 仅在首次创建时写入。
9. 对单台 GPU 服务器执行旧数据清理前，必须确认该服务器的 `server_gpu` 与 `gpu_uplink` 全部分页、必要接口解析和图写入均成功。任一环节失败时，跳过该服务器范围的全部 GPU 清理。
10. 当前上联记录引用的 GPU 或 ToR 接口无法解析时，不生成新边，也不得删除该记录可能对应的旧上联边；无法安全确定影响范围时，跳过该服务器的上联清理并记录原因。
11. 清理顺序为过期 `gpu_uplink` → 过期 `contains_gpu` → 无其他有效引用的过期 GPU 节点。删除 GPU 节点前必须确认上联范围完整，避免留下悬空边或误删仍被引用的 GPU。
12. 引用为空、身份冲突或目标无法唯一解析时，记录对象类型、服务器 SN、来源 UUID、错误类型和数量；不在 MySQL 保存原始关系候选。

## 7. 样例闭环

当前样例服务器：

```text
device_sn = 219685020073
device_type_id = 58
server_gpu count = 8
gpu_uplink count = 8
```

8 张 GPU 卡与 8 条上联在以下字段上一一对应：

```text
server_gpu.device_sn = gpu_uplink.device_sn
server_gpu.parts_sn  = gpu_uplink.gpu_sn
server_gpu.slot      = gpu_uplink.gpu_slot
server_gpu.conn_ports contains gpu_uplink.gpu_port
```

例如：

```text
GPU uuid:  b6ea16f9-e0a8-427a-a44e-8224e14f2087
parts_sn:  MY15960263P01637
slot:      204
conn_port: ib8
uplink uuid: 24572728-bd42-4acb-bb94-d02552cf8600
tor_sn:      210235A53L5264L100JC
tor_port:    FourHundredGigE1/0/11
```

该样例可确认 GPU 身份、所属设备和 GPU 侧上联匹配规则。只有查询到上述 `tor_sn + tor_port` 对应的稳定 `port_uuid` 后，才生成 `gpu_uplink` 图边。

## 8. 来源证据

- [device_server_gpu.md](../../cmdb/device_server_gpu.md)
- [device_server_gpu_uplink.md](../../cmdb/device_server_gpu_uplink.md)
- [device.md](device.md)
- [interface.md](interface.md)
- [asset-cabinet-mapping.md](../../cmdb/asset-cabinet-mapping.md)
