# T-006 独立集成验收记录

日期：2026-09-08。范围：FR-001、FR-002、FR-034 中由 T-006 实现的管理员初始化、认证会话、凭证边界、近期重认证、敏感读取审计、受控重置与数据库失效关闭。本记录是自动技术验收，不代替人工项目评审。

## 验收结论

| 检查 | 结论 | 实际操作与结果 |
| --- | --- | --- |
| 真实 HTTPS → HTTP handler → Identity → PostgreSQL 生命周期 | PASS | `TestPostgresIdentityHTTPSAcceptance` 在受信任测试证书、Cookie Jar 和独立 PostgreSQL 数据库上实际执行，1.24 秒通过；没有 skip。覆盖初始化、重建服务与 handler 后状态保持、再次初始化 409、登录轮换、`me`、`reauth`、`logout`。原始事件见 [go-test.jsonl](validation/go-test.jsonl)。 |
| 管理／订阅／Runner 凭证隔离 | PASS | 在已有有效管理 Cookie 的反例上分别加入 Bearer 凭证和实际 TLS 客户端证书，均返回 401；退出的另一会话失效且不撤销当前会话。 |
| 近期认证与敏感读取审计 | PASS | 未重认证的测试敏感路由返回 403 `REAUTH_REQUIRED`；显式密码重认证后才释放响应，并确认数据库恰有一条已提交的 `secret.reveal` 成功审计。 |
| 重置、撤销与数据库失效关闭 | PASS | 受控重置后旧 Cookie 与旧密码均返回 401，新密码可登录；关闭 runtime pool 后携带仍有效 Cookie 的 `me` 返回 503 `SERVICE_UNAVAILABLE`。同一最终运行复用的 `TestPostgresIdentityAdminResetCommand` 也实际通过。 |
| 明文凭证保护 | PASS | 独立用例检查错误响应和访问日志不含初始化凭证、密码、Session 或 CSRF 原值（显式会话响应按契约返回 CSRF）；数据库密码为限定 Argon2id 格式，Session 仅存 32 字节域隔离摘要且不等于 Session／CSRF 原始字节。证据和本记录均未记录这些原值。 |
| 未实现 API 路由 | PASS | 已认证访问仍为 contract-only 的 `/api/v1/nodes` 返回类型化 404，未落入 SPA 成功响应。 |
| 完整身份数据库正负例 | PASS（复用） | 同一冻结源码运行中的 9 组核心 PostgreSQL 身份测试全部通过，覆盖并发一次性初始化、生命周期与重置、过期／epoch／禁用、用户间会话隔离、持久限流、审计失败回滚、reset 与 login／reauth 竞争、最小权限和数据库丢失、摘要篡改／pepper／scope／实时用户版本。该部分是实现测试证据，本代理未冒称重复编写或独立执行。 |
| 隔离环境与清理 | PASS | 最终 verifier 使用锁定的 `postgres:17.6-bookworm` linux/amd64 镜像和分离数据库角色；20 个 PostgreSQL 用例全通过、`postgres_skipped_tests` 与 `failed_tests` 均为空，容器与网络清理均通过。见 [report.json](validation/report.json)。 |

最终结果适用于源码提交基点 `1ff4c094d9d268c8afae82a6987ba216892b2a46` 上、源码清单 SHA-256 `634e2e43f4b8c28febf423ecfc71f792137f06e4eb544bcb9c1cdd4f39aff0d9` 所绑定的工作树。独立用例文件在该清单中的 SHA-256 为 `25059272cf275aed4d81e9e1df748ad714e65f755cfc8a5696d9b187bed61220`。环境为 Go 1.26.0 windows/amd64 测试进程和 PostgreSQL 17.6 linux/amd64；命令为：

```text
go test -mod=readonly -json -count=1 -timeout=180s ./internal/catalog ./internal/storage ./internal/secretbox ./internal/apicontract ./internal/identity ./internal/server ./internal/config ./proxyloom-server ./api
```

本地无 DSN 的先行编译检查只证明测试可编译，曾明确显示 PostgreSQL case 为 skip，没有作为验收通过依据。第一次真实联验暴露测试专用敏感路由缺少生产 handler 外层的 server-owned Request ID boundary；修复测试夹具后才执行上述最终冻结源码运行。此前静态复核还发现数据库内部角色值与公开 API `administrator` 投影不一致，应用投影修复已纳入最终源码清单并由真实 HTTPS 用例验证。

## 文件与剩余范围

- 新增独立测试：`internal/storage/identity_http_acceptance_test.go`。
- 新增本记录：`docs/evidence/T-006/independent-acceptance.md`。
- 最终原始报告来自 `.cache/foundation/d7f4b61f-0abb-478c-b6a6-e0e211a525aa/`；集成主代理已将稳定副本归档到 T-006 `validation/`。
- 本代理未执行远端 CI、人工浏览器验收或最终 Linux Compose 启动检查；这些不能由本记录推断为通过。集成主代理负责结合后续检查作 T-006 总体验收判断。
