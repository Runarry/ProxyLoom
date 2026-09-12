# ProxyLoom · 织流

自托管的 Xray、sing-box、Mihomo 订阅管理与测试平台。本窗口接入六类节点管理、本地 URI／文本／Base64 导入、登录与管理界面、持久任务及独立 mTLS Runner 配置校验。逐项验收状态与限制见 `docs/PLAN.md` 和 `docs/evidence/`，实现存在不代表所有阶段门槛完成。

来源管理、刷新差异确认、人工覆盖恢复、节点 URI／Base64 导出及编排管理已接入。两跳链、策略组、路由、内联规则集与 DNS 提供类型化 API 和管理界面；系统预设可只读查看。六类节点可解析／保存与内核兼容分别展示；未经对应真实执行验证的组合仍为 `unverified`。订阅发布闭环已完成 M2 联验；网络测速按 M3 推进。新增契约见 `docs/m1-orchestration-contract.md`。

2026-09-10：M1／G1 已按用户批准范围收口，见 [G1 验收与范围决定](docs/reviews/2026-09-10-g1-decision.md)。sing-box 1.14.0 的 `resolve_for_ip_rules` 含有效 IP 条件时明确拒绝编译，保留 `preserve_domain`，不自动降级；Xray／Mihomo 的两种模式保持。2026-09-12：M2／G2 已按已验内核与预设范围收口，10 项任务完成，见 [M2 发布契约](docs/m2-publication-contract.md)与 [G2 验收决定](docs/reviews/2026-09-12-g2-decision.md)。桌面／移动客户端导入及 arm64 未验证。

基座提交 `1ff4c09` 的远端运行 [34201742618](https://github.com/Runarry/ProxyLoom/actions/runs/34201742618) 已通过；基座收口见 `docs/reviews/2026-09-08-m1-foundation-closeout.md`。该运行不代表本窗新增代码已在远端执行。

## 启动开发环境

需要 Docker Compose、PowerShell 7。镜像基线和工具版本锁在 `deploy/tools.lock.json`；Go 1.26.0、Node 24.6.0、pnpm 10.33.2 用于本机开发。

在仓库根运行：

```powershell
pwsh -NoProfile -File scripts/dev-init.ps1
docker compose -f deploy/compose.dev.yaml up --build -d --wait
```

打开 http://127.0.0.1:8080 。API 只监听宿主机回环；PostgreSQL 和 Runner 没有宿主机端口。空 Runner 不领取任务、不运行内核，也没有数据库或主密钥挂载。停止服务使用 `docker compose -f deploy/compose.dev.yaml down`，不删除数据库卷。

第一次初始化显式创建独立随机开发秘密和 setup_token。再次执行保留现有值；原八项秘密不完整时失败，不自动轮换。升级已有完整开发秘密组时，仅创建缺失的 setup_token；数据库一次性初始化状态不会因此重置。秘密保存在被 Git、Docker 构建上下文排除的 `deploy/secrets/local/`。Windows 需要能设置该目录 ACL 的当前用户；Unix 使用 0700 目录和可供非 root 容器读取的文件。已有数据库卷应保留配套密码文件。

`PROXYLOOM_DEV_PORT` 可修改回环端口。开发 Compose 显式启用 `PROXYLOOM_DEV_MODE=true` 允许 HTTP 回环 PUBLIC_URL；正常模式要求 HTTPS。该例外允许本地开发认证 Cookie 省略 Secure，仅适用于显式开发模式和 HTTP 回环 PUBLIC_URL；生产必须使用 HTTPS。

## 管理员初始化与认证

首次启动可在前端初始化页面输入秘密文件中的 setup_token、用户名和密码；完成后使用登录页面进入节点管理。API 初始化仍为 `POST /api/v1/setup`，必须带与 PUBLIC_URL 相同的 Origin 及 `Content-Type: application/json`。不要把真实凭证放入示例脚本、终端历史或日志。

登录接口为 `/api/v1/auth/login`；客户端使用 HttpOnly Cookie 并将返回的 csrf_token 仅存内存。后续写请求携带 `X-CSRF-Token` 和 Origin。`/auth/me` 可恢复用户状态，`/auth/reauth` 提供五分钟敏感操作认证，`/auth/logout` 吊销会话。

生产初始化凭证通过受控命令生成到绝对路径，再以 `PROXYLOOM_SETUP_TOKEN_FILE` 注入；初始化完成后可移除配置与挂载。没有此配置时 setup 被禁用，其他认证功能可继续使用。

```text
proxyloom-server admin create-setup-token --output /secure/proxyloom/setup_token
proxyloom-server admin reset-password --username admin --password-file /secure/proxyloom/new_password
```

父目录需限制为授权操作者可访问；容器 UID 必须有对应单文件读取权限。重置使用 runtime DSN 与 token pepper 文件，吊销所有旧管理员会话。没有公共注册或远程忘记密码入口。配置、权限和契约详见 `docs/m1-identity-contract.md`。

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

完整质量入口和兼容性变更声明见 `docs/quality-gates.md`。本地完整验收可运行 `node scripts/check-quality.mjs`；2026-09-10 已为 master 启用 `checks` 与 `development-smoke` 必需检查，回读证据见 `docs/evidence/T-008/required-checks-2026-09-10.md`。远端检查成功仍须对应实际代码提交。

`verify-foundation.mjs` 每次创建独立 Docker bridge 网络、随机凭证和 tmpfs PostgreSQL，仅发布回环端口，不复用开发卷；测试结束后按所有权标签清理本次容器与网络。报告与 Go 事件保存在 `.cache/foundation/<run_id>/`。其数据库测试必须实际执行，不能把未提供数据库的单测 skip 当作验收通过。

主密钥要求显式 `PROXYLOOM_MASTER_KEY_ID`，开发 Compose 使用 `dev-master-v1`。三个原有秘密文件仍是独立 32 字节原始值；可选 `PROXYLOOM_OLD_MASTER_KEYS_FILE` 提供旧 key_id 到绝对文件路径的严格 JSON 映射。详情和 API 十进制计数契约见 `docs/adr/0007-m1-foundation.md`。

Windows 未安装 CGO 编译器时，使用 `docker build -f deploy/Dockerfile.check .` 在锁定的 Linux 工具链中运行同一检查入口；源码只进入开发检查镜像，秘密目录被构建上下文排除。

锁定内核配置检查：`node scripts/verify-compile-exec.mjs`。隔离 Compose 烟测：`node scripts/verify-isolation-compose.mjs`。真实链路矩阵：`node scripts/verify-live-chain.mjs`（临时 Linux 容器，不关闭 TLS 校验）。这些不把能力标为 `verified`，也不代替 G0 人工评审。

受限环境可以把 `GOPATH`、`GOMODCACHE`、`GOCACHE` 分别放在仓库 `.cache/gopath`、`.cache/gomod`、`.cache/go-build`；不要关闭 Go 校验数据库或 TLS 校验来绕过缓存权限。

## 工程与接口

根目录单 Go module，两个独立入口：`go build -o bin/proxyloom-server ./proxyloom-server`、`go build -o bin/proxyloom-runner ./proxyloom-runner`。Windows 可为产物加 `.exe`。Vue/Vite 代码独立构建后装入 API 镜像。

- API：`serve`、`healthcheck`、`migrate up`、`migrate status`。健康检查不读取秘密；`/healthz` 只表示进程存活，`/readyz` 检查数据库、迁移与必要配置。
- 迁移：独立 `PROXYLOOM_MIGRATION_DSN_FILE`；运行账号使用 `PROXYLOOM_DATABASE_DSN_FILE`，具有必要业务列权限但不能改写迁移元数据或不可变历史。已执行迁移按内容摘要校验，仅追加新文件。
- IR：本地嵌入 JSON Schema v1、Go 类型与语义校验；两跳具体节点、严格字段、稳定引用、冻结快照。接口与例子见 `docs/ir-contract.md`。
- 内核锁：`compat/cores.lock.yaml` 固定 Xray／sing-box／Mihomo 的 linux amd64 与 arm64 摘要；`adapter_version` 为 `0.2.0-m1-orchestration`。`internal/capability` 在版本、摘要或架构不符时拒绝；能力证据按具体组合和架构记录。
- 编译：`internal/compiler` 从冻结节点、链、策略、路由、DNS、规则集与预设生成三目标配置，支持类型化目标策略覆盖。不等价的字段显式拒绝，最终能力范围以 T-029 的实际证据为准。`node scripts/verify-compile-exec.mjs` 回归锁定 linux/amd64 原型配置检查。
- M1 原生矩阵：`node scripts/verify-m1-native.mjs` 检查 72 份完整配置及对应损坏反例，并运行手动切换、轮询、低延迟、路由、IP 解析与故障不直连夹具。证据见 `docs/evidence/T-029/`，不能外推全部参数或客户端兼容。
- 客户端预设：三个 Linux 文件预设使用 SOCKS `127.0.0.1:1080`；另有用户批准的 sing-box／Mihomo 回环控制变体，控制端口分别为 `17812`／`17813`。预设不可编辑、初始化幂等，控制状态在列表中可见；审核状态不等于实际客户端导入验证。
- 隔离夹具：`internal/isolation` 与 `deploy/compose.isolation.yaml` 仅用于测试，不随开发 Compose 启动。容器烟测：`node scripts/verify-isolation-compose.mjs`。
- 真实链路（T-028）：`node scripts/verify-live-chain.mjs` 在临时 Linux 容器中冻结夹具地址、Compile、校验并经回环入口探测。不关闭 TLS 校验。限定 G0 已获用户批准，其他架构和组合仍需后续验收。
- 管理认证、节点、导入及任务接口已接入，内部 Runner 使用单独 mTLS 监听器；具体清单见 `fixtures/api/implemented-routes.json`。未知 `/api`、`/internal`、`/s` 路径不会返回前端成功页面。

需求依据、范围与 ADR 见 `docs/baseline/README.md`。机器任务清单为 `docs/PLAN.tasks.json`，执行主入口为 `docs/PLAN.md`。逐任务证据在 `docs/evidence/`，首轮完成不代表 G0 或三内核兼容验证通过。
