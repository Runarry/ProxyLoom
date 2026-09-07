# T-002 工程骨架与开发环境证据

状态：实现及开发烟测通过，待最终评审。不是 AC-21 全量验收、生产部署或 G0 结论。

## 追溯与环境

- 任务：T-002；FR-036 开发部署子集、AC-21 开发烟测子集；秘密及诊断检查只覆盖当前工程壳，不代表 AC-23 全量完成。
- 起始提交：`8acf714`；本次实现尚未提交，证据对应 working tree。最终文件清单见同目录 `source-manifest.json`。
- 主机：Windows amd64，Go 1.26.0、Node 24.6.0、pnpm 10.33.2；Docker Engine 29.7.2、Compose 5.5.0；实际容器环境 linux/amd64。
- 工具和官方基础镜像摘要：`deploy/tools.lock.json`；依赖完整性：`go.sum` 与前端 `pnpm-lock.yaml`。
- 执行人：Codex；独立代码复核：foundation_review 子代理；人工基线／合并评审未执行，没有虚构审核人。

## 已实际执行

1. 前端 `pnpm install --frozen-lockfile`、`pnpm typecheck`、`pnpm build` 通过；Vite dev 启动及 localhost HTTP 200，确认只监听 127.0.0.1:5173，检查后已停止。未执行浏览器交互验收，未为静态页面增加复述实现的测试。
2. 服务相关七个 Go 包的单元测试及 `go vet` 通过。包括配置缺失／错误大小／密钥复用、公共地址约束、Runner 禁用环境变量、HEAD／静态文件／保留路由、日志与 panic 脱敏、数据库就绪超时、迁移版本与摘要不匹配等。
3. `docker compose -f deploy/compose.dev.yaml build api runner` 在固定 Linux 基础镜像中实际构建 API、前端和 Runner；Go module 下载验证通过，没有构建或下载代理内核。
4. `pwsh -NoProfile -File scripts/dev-init.ps1` 显式生成开发秘密；另在隔离临时目录执行首次初始化和重复初始化，确认生成 8 个独立文件，重复执行保持每个文件字节不变。真实值未输出、未提交，Git 与 Docker 构建排除项已检查。
5. `pwsh -NoProfile -File scripts/smoke.ps1 -Build` 完整通过。归档的实际运行报告为 `smoke-report.json`；时间为 2026-09-07T03:57:01Z～03:57:46Z，使用本次新建的随机 Compose 项目和真实 PostgreSQL。
6. 使用本次创建的带项目 label 的临时网络运行已有资源负例，烟测拒绝执行；再次检查网络 ID 未改变，随后只移除该测试网络。
7. `node scripts/check-plan.mjs`、`node scripts/check-boundaries.mjs`、`node scripts/check-locks.mjs` 通过。工具锁另通过 7 项内存漂移反例，均失败关闭，没有修改仓库作为测试手段。

烟测覆盖：API readiness/liveness、前端 HTTP、三个保留路径 404、重复迁移及两个真实迁移进程并发、运行账号权限限制、缺失主密钥启动失败、Runner 停机独立性、数据库停机 readiness 503/liveness 200、恢复及 API 重启、Runner 无数据库网络／秘密挂载／发布端口、服务退出码 0、捕获日志中无本次开发秘密（原文、Base64、大小写 hex）。这项扫描不宣称覆盖任意编码或未来所有业务秘密。

## 故障发现与修复

- Compose flow-list 未加引号导致 tmpfs 参数被拆为多个路径：修复为完整字符串，重新构建并实际启动验证。
- HEAD 错误响应被 Gin 的默认 404 写入响应体：修正通用响应并增加回归测试。
- Windows sandbox 与 Docker Desktop 用户不同，初始 ACL 阻止挂载：改为只操作 DACL，授权当前用户／SYSTEM／Administrators，以及已确认仓库所有者的只读权限；不向 Users／Authenticated Users 开放。
- 独立审查发现固定烟测项目名可能影响其他运行：改为随机名称、现有资源预检查，仅清理本次新建资源，并执行保护负例。
- 工具锁原为重复文档记录：接入实际 CI 漂移检查，与 Dockerfile、Compose、Go、前端和 CI 比较。

## 边界与后续

开发 Compose 显式允许回环 HTTP；管理认证未实现，相关路由均不开放。Runner 是 idle 健康服务，未登记、未启用 mTLS、队列或进程执行。数据库只有迁移元数据表；加密功能、业务表和正式 OpenAPI 留后续任务。

未执行远程 GitHub Actions、arm64 原生烟测、正式升级回退、恢复演练、真实代理内核或网络测试。Linux race 检查的最终结果补记于本轮集成记录，不能由 Windows 普通单测推断通过。

## 最终集成补记

`docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-dev .` 已通过，实际执行 `node scripts/check.mjs`：锁一致性、60 项计划检查、模块依赖边界、gofmt、模块验证、tidy 无漂移、go vet 以及 Linux 全部 Go race 测试均通过。检查镜像 ID 为 `sha256:4109fbdcc67140dbc9f69626e0323a043ef3d667e89f8a0605e524d36217e4e7`；这是本地检查产物，未发布。

Windows 两个独立二进制也通过 `go build -mod=readonly -trimpath` 构建。烟测使用的新项目资源及早期烟测遗留卷均已清理，没有启动或修改其他项目服务。本轮任务状态已进入 `in_review`，待人工评审，不自动标 done。
