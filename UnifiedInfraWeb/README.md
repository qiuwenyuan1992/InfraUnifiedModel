# UnifiedInfraWeb · 统一基础设施管理台

Vue 3 + TypeScript + Vite + Element Plus 独立管理前端，读取真实 `UnifiedInfraTopology` 同步管理 API。界面为中文，第一期不包含登录、用户、权限管理、JWT、浏览器存储或模拟业务数据。

## 后端服务

后端位于平级 `UnifiedInfraTopology/`；本地开发配置已关闭管理API鉴权并配置私密游标签名密钥，修改开关后要重启HTTP服务。分别运行：

```bash
# 在 UnifiedInfraTopology 目录
go run ./cmd/server -conf config/local.yml
go run ./cmd/worker -conf config/local.yml -process-rounds
```

仅浏览已有任务不需要启动worker；创建后要执行同步才需要worker。任务取消不回滚已写图库，默认没有旧数据清理。不要将免鉴权服务暴露到公共网络。

## 本地启动

推荐 Node.js 22.12+（22 LTS）或 24 LTS，npm 10+。

```bash
npm ci
cp .env.example .env.local
npm run dev
```

开发服务仅监听 `127.0.0.1`。Vite 将 `/v1` 原路径代理至 `VITE_API_PROXY_TARGET`，默认 `http://127.0.0.1:8000`，已核对后端 `config/local.example.yml` 的 `http.host` / `http.port`。无需修改前端 API 路径。`.env.local` 不提交；`VITE_` 变量可能进入浏览器构建，禁止配置任何真实秘密。

后端应已完成控制库迁移并启动 HTTP 服务。开发时在独立本地配置设置：

```yaml
inventory:
  auth:
    enabled: false
```

此配置依赖后端提供相应的本地鉴权开关；本项目不修改后端配置。不要在非本地部署中关闭鉴权或公开绑定。签名分页仍需配置后端私密 `inventory.cursor_key`，不要将其写入前端。401/403 提示后端本地鉴权配置问题，不提供令牌兜底。

HTTP 创建请求仅持久化入队，实际执行需要已获授权的独立 `-process-rounds` Worker。本项目安装、测试、构建均不会创建真实任务、启动 Worker、调用 CMDB 或清理图数据。

## 已实现

- `GET /v1/inventory/sources`：读取全部游标页，选择真实已启用来源；只读来源清单。
- 整轮任务列表：来源 / 状态筛选、20–200 条页大小、签名游标上一页 / 下一页；刷新保留当前游标及筛选，筛选变更回到第一页。
- 仅用户确认创建才 `POST /sync-rounds`，请求体固定 `{source_id, mode:"full"}`；每次新提交用 `crypto.randomUUID()` 生成幂等键，失败重试保留同一键，不自动重试写请求。
- 详情展示后端 15 阶段、成功阶段比例、时间、错误码、阶段计数与整轮/子任务诊断入口。活动阶段计数标为 `—`，不把未完成零值解释为来源为空。
- 取消必须二次确认；运行中的取消等待 Worker 响应，不承诺回滚已写入图数据。
- 诊断抽屉独立保留游标与来源筛选，不发送状态筛选；只展示 API 已脱敏字段。
- 列表每 10 秒刷新当前页；仅选中的活动任务详情每 10 秒刷新，终态/关闭抽屉后停止；页面不可见时跳过轮询。定时器清理、请求互斥、旧响应隔离与 AbortController 取消防止重复请求和串页。
- loading / 空态 / 网络或后端错误均有明确提示。本页统计来自当前后端结果，不显示伪造的全量总数；后端未提供总数，不使用数字页码猜测总页数。

## 本次验收

后端全量测试、鉴权/服务竞态检查、静态检查和三个入口构建通过；前端14个单元测试、TypeScript检查与生产构建通过。独立审查发现的创建弹窗停用来源残留选择问题已修复：来源失效清空选择、按钮禁用、提交前再次校验。

浏览器使用临时Vite代理与真实本地API/MySQL，在无JWT模式下读取任务列表、成功任务15阶段详情和整轮诊断通过；没有创建/取消真实任务或启动同步worker。临时服务已关闭，没有保存开发报告到仓库；需要使用时按上方命令启动。

## 验证与构建

```bash
npm test
npm run build
npm audit
npm run preview
```

`build` 先执行 `vue-tsc --noEmit`，再生成 `dist/`。Vitest 仅替换测试中的 HTTP 传输响应验证 API 契约，不存在运行时 mock 后端。锁文件固定依赖；`.npmrc` 使用 `legacy-peer-deps` 避免本地 npm 10 在升级 Vitest 可选 peer 图时的 `edgesOut` 异常。

`preview` 仅用于预览构建产物，不提供开发 API 代理。部署时应由同源反向代理将 `/v1` 转发至后端；本期无生产鉴权方案，不能直接公开部署本地免鉴权控制台。
