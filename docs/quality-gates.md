# 持续质量检查（T-008）

统一入口为仓库根目录执行 `node scripts/check-quality.mjs`。它要求
`deploy/tools.lock.json` 中锁定的 Node、Go、pnpm，以及 Linux Docker 引擎；
Go race 测试还需要平台适用的 C 编译器。入口执行锁定的 sqlc 安装校验和
`pnpm install --frozen-lockfile`，不会更新依赖锁。
冻结安装子进程设 `CI=true` 以支持本地非交互执行。受限环境可将
`GOCACHE`／`GOMODCACHE` 指向工作区 `.cache/go-build`／`.cache/gomod`。

入口按顺序运行：工作区和暂存区秘密扫描、工具锁、自检反例、既有
`check.mjs`（PLAN／模块边界／格式／依赖／sqlc／内核锁／Go vet／Go race）、
前端类型检查和构建、OpenAPI 类型重建比较、契约基线差异、独立 PostgreSQL
foundation 与 identity 验收。开发 Compose 仍使用现有独立烟测作业。
任一子进程非零退出、超时、输出超限或秘密扫描失败都会保留失败，并将后续步骤
记录为 `not_run`。检查不会修改 PLAN 状态或 capability 状态。

本地仅检查源码时可执行 `node scripts/check-quality.mjs --profile source`；
报告明确标为 `source-only-postgres-not-run`，不能作为完整数据库验收。
`--base <commit-or-ref>` 用于选择差异基线，本地默认 `HEAD`（包含尚未提交的改动）。
CI 的 PR 使用目标分支 SHA，普通 push 使用前一 SHA，首次 push 无前一提交时
使用 `HEAD`。CI 完整获取历史；缺失或无效基线会失败，不退回空差异。

## 契约差异

`node scripts/check-contracts.mjs --base <ref>` 比较 `api/openapi.yaml`、
`schemas/**/*.schema.json` 以及所有 `migrations/**/*.sql`。
既有迁移不能修改或删除。新迁移也必须声明并同步生成查询。

这是保守的变更审阅约束：描述、认证元数据、数组、字段约束及新增内容均须显式声明，
不声称自动解决所有 OpenAPI／JSON Schema 的兼容关系。声明放在
`compat/contract-changes/T-xxx.json`，内容包括任务、行为说明、变更的文档、
夹具、生成文件，以及每份契约变更前后的完整源码 SHA-256 和 JSON Pointer
差异摘要。只归一化 CRLF；完整文件摘要同时防止大整数解析精度导致漏检。

每一变更必须且只能匹配一条准确声明。声明和所列文档、夹具必须在同一基线差异中
变化；API 必须同步 `proxyloom-web/src/api/schema.d.ts`，迁移必须同步
`internal/storage/generated/*.go`。声明不能用路径通配符或只列 JSON Pointer
放行未来变化；额外修改会使摘要失配。`check-api.mjs` 和 `check-sqlc.mjs`
分别在独立临时目录重建产物并比较，不能通过自动修复受检文件掩盖漂移。
添加或调整声明仍需正常代码审查。

## 秘密与证据

`node scripts/check-secrets.mjs` 扫描 Git 跟踪文件、未忽略的候选文件，以及
每个暂存区 blob，避免工作区的干净版本掩盖即将提交的秘密。不会跳过夹具、
文档、证据目录或二进制／UTF-16 文件。每文件上限 16 MiB，暂存区批读上限
128 MiB，超限失败。忽略的开发秘密不是提交候选；它们不能作为任意附件上传。

规则覆盖 PEM 私钥、规范 `sub_<public_id>.<random>` 订阅令牌、常见订阅令牌
赋值、订阅路径、带凭证 URI、编码代理 URI、Authorization、会话 Cookie 和
高置信度秘密字面量。无法靠模式识别的生成值必须额外注册：
`assertSecretFree(output, { path, secrets: generatedValues })`。注册值精确匹配
永不接受例外。扫描器只报告规则、文件、行号及用于审阅的整行摘要，不打印原文、
秘密值或注册值摘要；不能代替运行时不记录秘密的设计。

合成样例例外登记在 `fixtures/quality/secret-exceptions.json`，每项固定文件、
规则、整行 SHA-256 和审阅理由。换文件、改变整行或新增值均须重新审查。
没有目录豁免或全局“示例密码”白名单；例外不能用于运行日志。

质量报告在 `.cache/quality/<uuid>/`，只保存步骤元数据及源码摘要。
报告检查执行前后的受检源码清单，执行期间的变更会使结果失败。
`node scripts/check-quality-evidence.mjs` 在全部内容扫描成功后才新建
`.cache/quality-upload/`。允许的输入只有质量 report／source-manifest 与本次
报告引用的 foundation report／source-manifest／go-test.jsonl／go-stderr.txt；
最多 64 个文件、合计 64 MiB，不跟随越界符号链接，不复用旧上传目录。
collector 只为精确扫描读取 foundation 生成凭证，凭证文件永不复制或上传。
CI 只上传 collector 成功暂存的目录，保留 7 天。失败证据继续保留失败状态。

## 自检与合并边界

`node --test scripts/quality-secrets.test.mjs scripts/quality-contracts.test.mjs scripts/quality-runner.test.mjs`
在独立临时 Git 仓库／文件副本中验证：实际失败测试的非零退出、真实 API
生成漂移、未知破坏性修改、缺失配套文件、既有迁移变更、候选和暂存区秘密、
精确例外失效、运行输出秘密及附件白名单。临时生成的秘密不会写入仓库或测试日志。

工作流稳定作业名称仍为 `checks` 和 `development-smoke`；失败作业本身会失败，
没有 `continue-on-error`。仓库是否强制这些检查，依赖 GitHub 分支保护／ruleset。
2026-09-08 只读查询 `repos/Runarry/ProxyLoom/branches/master/protection` 返回
HTTP 401，因而远端强制合并保护尚未核实；本次未修改远端规则。
本地自检不证明新提交已在 GitHub 执行，也不代签人工验收或提升任务为 done。
