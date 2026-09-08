# ProxyLoom · 织流

自托管的 Xray、sing-box、Mihomo 订阅管理与测试平台。M0 已完成限定范围的原型与 G0 评审，当前进入 **M1 首窗：T-004 资源修订、T-005 秘密管理、T-007 API 契约**。已有三内核 Trojan TCP/TLS 配置生成和 linux/amd64 隔离链路证据；本窗业务功能通过内部服务验收，尚未开放登录、节点 CRUD、订阅发布或持久 Runner 调度。全部内核能力仍为 `unverified`。

2026-09-08 M0 修复补验后，用户已批准 linux/amd64 Trojan TCP/TLS 原型与临时容器测试 CA 方案，关闭 G0 并启动 T-004／T-005／T-007。批准记录见 `docs/reviews/2026-09-08-g0-decision.md`；M1 首窗提供资源修订、加密与 API 契约基座，验收状态以 `docs/PLAN.md` 和各任务证据为准。能力保持 `unverified`，不扩大到其他架构、协议或客户端。

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
node scripts/install-sqlc.mjs
node scripts/check-sqlc.mjs
node scripts/check-api.mjs
node scripts/verify-foundation.mjs
pwsh -NoProfile -File scripts/smoke.ps1 -Build
```

烟测每次使用带随机后缀的独立 `proxyloom-smoke-*` Compose 项目，默认回环端口 18080；若指定项目名已存在任何容器、网络或卷则拒绝运行。它检查重复及并发迁移、最小数据库权限、秘密缺失启动失败、数据库故障与恢复、Runner 停机独立性、保留路由 404、日志脱敏和正常退出；结束后只清理本次新建的测试容器、网络和卷。`-KeepRunning` 可保留成功运行供检查。脱敏运行报告写到被忽略的 `deploy/smoke-report.json`。

Linux（或具备 CGO 编译器的 Windows）在安装锁定 sqlc 后可运行 `node scripts/check.mjs`，包括格式检查、依赖漂移、生成查询检查、静态分析和 `go test -race`。GitHub Actions 同时运行 Go 检查、前端构建、API 类型漂移、真实 PostgreSQL 集成及 Compose 烟测。普通本机 `go test` 与 Linux race 结果应分别记录，不以跳过的检查宣称通过。

`verify-foundation.mjs` 每次创建独立 Docker bridge 网络、随机凭证和 tmpfs PostgreSQL，仅发布回环端口，不复用开发卷；测试结束后按所有权标签清理本次容器与网络。报告与 Go 事件保存在 `.cache/foundation/<run_id>/`。其数据库测试必须实际执行，不能把未提供数据库的单测 skip 当作验收通过。

主密钥现在要求显式 `PROXYLOOM_MASTER_KEY_ID`，开发 Compose 使用 `dev-master-v1`。三个原有秘密文件仍是独立 32 字节原始值；可选 `PROXYLOOM_OLD_MASTER_KEYS_FILE` 提供旧 key_id 到绝对文件路径的严格 JSON 映射。详情和 API 十进制计数契约见 `docs/adr/0007-m1-foundation.md`。

Windows 未安装 CGO 编译器时，使用 `docker build -f deploy/Dockerfile.check .` 在锁定的 Linux 工具链中运行同一检查入口；源码只进入开发检查镜像，秘密目录被构建上下文排除。

锁定内核配置检查：`node scripts/verify-compile-exec.mjs`。隔离 Compose 烟测：`node scripts/verify-isolation-compose.mjs`。真实链路矩阵：`node scripts/verify-live-chain.mjs`（临时 Linux 容器，不关闭 TLS 校验）。这些不把能力标为 `verified`，也不代替 G0 人工评审。

受限环境可以把 `GOPATH`、`GOMODCACHE`、`GOCACHE` 分别放在仓库 `.cache/gopath`、`.cache/gomod`、`.cache/go-build`；不要关闭 Go 校验数据库或 TLS 校验来绕过缓存权限。

## 工程与接口

根目录单 Go module，两个独立入口：`go build -o bin/proxyloom-server ./proxyloom-server`、`go build -o bin/proxyloom-runner ./proxyloom-runner`。Windows 可为产物加 `.exe`。Vue/Vite 代码独立构建后装入 API 镜像。

- API：`serve`、`healthcheck`、`migrate up`、`migrate status`。健康检查不读取秘密；`/healthz` 只表示进程存活，`/readyz` 检查数据库、迁移与必要配置。
- 迁移：独立 `PROXYLOOM_MIGRATION_DSN_FILE`；运行账号使用 `PROXYLOOM_DATABASE_DSN_FILE`，具有必要业务列权限但不能改写迁移元数据或不可变历史。已执行迁移按内容摘要校验，仅追加新文件。
- IR：本地嵌入 JSON Schema v1、Go 类型与语义校验；两跳具体节点、严格字段、稳定引用、冻结快照。接口与例子见 `docs/ir-contract.md`。
- 内核锁：`compat/cores.lock.yaml` 固定 Xray／sing-box／Mihomo 的 linux amd64 与 arm64 摘要；`adapter_version` 为 `0.1.0-m0-native`。`internal/capability` 在版本、摘要或架构不符时拒绝。能力保持 unverified。
- 编译：`internal/compiler` 复用 `Prepare` 后由三家族 Emit 输出完整原生配置。当前只映射 Trojan／native_tcp／TLS；`node scripts/verify-compile-exec.mjs` 用锁定 linux/amd64 内核检查生成字节。
- 隔离夹具：`internal/isolation` 与 `deploy/compose.isolation.yaml` 仅用于测试，不随开发 Compose 启动。容器烟测：`node scripts/verify-isolation-compose.mjs`。
- 真实链路（T-028）：`node scripts/verify-live-chain.mjs` 在临时 Linux 容器中冻结夹具地址、Compile、校验并经回环入口探测。不关闭 TLS 校验。限定 G0 已获用户批准，其他架构和组合仍需后续验收。
- 管理 API、Runner mTLS/队列和发布分别由后续任务提供；未知 `/api`、`/internal`、`/s` 路径不会返回前端成功页面。

需求依据、范围与 ADR 见 `docs/baseline/README.md`。机器任务清单为 `docs/PLAN.tasks.json`，执行主入口为 `docs/PLAN.md`。逐任务证据在 `docs/evidence/`，首轮完成不代表 G0 或三内核兼容验证通过。
