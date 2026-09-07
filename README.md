# ProxyLoom · 织流

自托管的 Xray、sing-box、Mihomo 订阅管理与测试平台。当前实现 **WP-01 首轮 M0（T-001～003）工程与 IR 基座**：可以运行空 API、中文工程页、空 Runner 和 PostgreSQL 迁移入口；还没有登录、节点 CRUD、订阅发布或内核执行功能。

## 启动开发环境

需要 Docker Compose、PowerShell 7。镜像基线和工具版本锁在 `deploy/tools.lock.json`；Go 1.26.0、Node 24.6.0、pnpm 10.33.2 用于本机开发。

在仓库根运行：

```powershell
pwsh -NoProfile -File scripts/dev-init.ps1
docker compose -f deploy/compose.dev.yaml up --build -d --wait
```

打开 http://127.0.0.1:8080 。API 只监听宿主机回环；PostgreSQL 和 Runner 没有宿主机端口。空 Runner 不领取任务、不运行内核，也没有数据库或主密钥挂载。停止服务使用 `docker compose -f deploy/compose.dev.yaml down`，不删除数据库卷。

第一次初始化显式创建独立随机开发秘密。再次执行保留现有值；缺失或不完整时失败，不自动轮换。秘密保存在被 Git、Docker 构建上下文排除的 `deploy/secrets/local/`。Windows 需要能设置该目录 ACL 的当前用户；Unix 使用 0700 目录和可供非 root 容器读取的文件。已有数据库卷应保留配套密码文件。

`PROXYLOOM_DEV_PORT` 可修改回环端口。开发 Compose 显式启用 `PROXYLOOM_DEV_MODE=true` 允许 HTTP 回环 PUBLIC_URL；正常模式要求 HTTPS。这一例外不开放任何管理员或秘密读取 API。

## 检查与烟测

```powershell
go mod download
go mod verify
go vet ./...
go test ./...
pnpm --dir proxyloom-web install --frozen-lockfile
pnpm --dir proxyloom-web typecheck
pnpm --dir proxyloom-web build
node scripts/check-plan.mjs
node scripts/check-boundaries.mjs
pwsh -NoProfile -File scripts/smoke.ps1 -Build
```

烟测每次使用带随机后缀的独立 `proxyloom-smoke-*` Compose 项目，默认回环端口 18080；若指定项目名已存在任何容器、网络或卷则拒绝运行。它检查重复及并发迁移、最小数据库权限、秘密缺失启动失败、数据库故障与恢复、Runner 停机独立性、保留路由 404、日志脱敏和正常退出；结束后只清理本次新建的测试容器、网络和卷。`-KeepRunning` 可保留成功运行供检查。脱敏运行报告写到被忽略的 `deploy/smoke-report.json`。

Linux（或具备 CGO 编译器的 Windows）可运行 `node scripts/check.mjs`，包括格式检查、依赖漂移、静态分析和 `go test -race`。GitHub Actions 同时运行 Go 检查、前端构建及 Compose 烟测。普通本机 `go test` 与 Linux race 结果应分别记录，不以跳过的检查宣称通过。

Windows 未安装 CGO 编译器时，使用 `docker build -f deploy/Dockerfile.check .` 在锁定的 Linux 工具链中运行同一检查入口；源码只进入开发检查镜像，秘密目录被构建上下文排除。

受限环境可以把 `GOPATH`、`GOMODCACHE`、`GOCACHE` 分别放在仓库 `.cache/gopath`、`.cache/gomod`、`.cache/go-build`；不要关闭 Go 校验数据库或 TLS 校验来绕过缓存权限。

## 工程与接口

根目录单 Go module，两个独立入口：`go build -o bin/proxyloom-server ./proxyloom-server`、`go build -o bin/proxyloom-runner ./proxyloom-runner`。Windows 可为产物加 `.exe`。Vue/Vite 代码独立构建后装入 API 镜像。

- API：`serve`、`healthcheck`、`migrate up`、`migrate status`。健康检查不读取秘密；`/healthz` 只表示进程存活，`/readyz` 检查数据库、迁移与必要配置。
- 迁移：独立 `PROXYLOOM_MIGRATION_DSN_FILE`；运行账号使用 `PROXYLOOM_DATABASE_DSN_FILE`，只有连接、schema 使用及迁移元数据读取权限。已执行迁移按内容摘要校验，仅追加新文件。
- IR：本地嵌入 JSON Schema v1、Go 类型与语义校验；两跳具体节点、严格字段、稳定引用、冻结快照。接口与例子见 `docs/ir-contract.md`。
- 管理 API、Runner mTLS/队列、真实编译和网络执行分别由后续任务提供；未知 `/api`、`/internal`、`/s` 路径不会返回前端成功页面。

需求依据、范围与 ADR 见 `docs/baseline/README.md`。机器任务清单为 `docs/PLAN.tasks.json`，执行主入口为 `docs/PLAN.md`。逐任务证据在 `docs/evidence/`，首轮完成不代表 G0 或三内核兼容验证通过。
