# T-006 管理员认证与信任边界

日期：2026-09-08。状态：`in_review`，本地自动验收通过，待本窗最终评审；不代表 G1 或生产发布。

## 实现

追加迁移 000004，实现独立身份服务／pgx/sqlc 仓储、一次性 setup、Argon2id 密码、HMAC 会话摘要与 CSRF、数据库实时版本／期限校验、账户和 IP 持久限速、最小追加审计及受控主机重置。五个认证 HTTP 接口已挂载，未开放其他业务接口。

事务使用 P0 身份状态锁使初始化、密码验证、会话签发、重置和审计串行化；限速计入所有认证尝试（包括成功），初值为每账户每分钟 5 次、每 IP 每分钟 30 次，最多 16384 个活动限速键，过期键清理；密码工作进程内最多两个 Argon2 执行槽。会话与认证活动不改变业务目录修订。细节见 ADR-0008 和 `docs/m1-identity-contract.md`。

## 实际验证

| 验证 | 结果和边界 |
| --- | --- |
| 真实 PostgreSQL 综合验收 | [report.json](validation/report.json)：20 组全通过，无 skip／fail，容器与网络清理通过。包含 9 组身份核心、独立 HTTPS 和实际主机 CLI，共 11 组身份测试。 |
| 独立真实 HTTPS 接入 | [独立验收](independent-acceptance.md)：受信任测试证书、Cookie Jar、Origin／CSRF、凭证不可互用、显式重认证和审计、服务重建、重置及 DB 故障；验证 TLS 未关闭。 |
| Linux 工程与 race | `docker build -f deploy/Dockerfile.check -t proxyloom-check:t006-t008 .` 成功；[构建检查输出](validation/linux-engineering.txt)含实际 Go vet／race、生成查询及边界结果。此检查未注入 DSN，其数据库 skip 不当作实库通过。 |
| Linux API Compose | [报告](validation/compose-smoke-report.json)、[输出](validation/compose-smoke.txt)：31 项通过，清理 pass。实际编译二进制完成初始化／重认证／CSRF／DB 故障／重启会话／退出；Runner 无 DB 网络或秘密挂载。 |
| 前端和 API | 前端 typecheck/build、OpenAPI 重新生成与编译校验通过；没有实现登录页面。统一质量入口的最终记录见 T-008。 |
| 秘密与历史保护 | 仓库扫描通过；新增密码、Cookie、CSRF、setup 凭证不记录为证据。旧迁移未改写；内核 IR／编译器／核心锁未变。 |

原始实库运行 `d7f4b61f-0abb-478c-b6a6-e0e211a525aa` 绑定基点 `1ff4c094d9d268c8afae82a6987ba216892b2a46` 上的工作树，291 文件源码清单在 [source-manifest.json](validation/source-manifest.json)，验收开始与结束内容一致。后续质量脚本和烟测收尾不改变该清单中的身份应用和验收代码；最终统一质量入口另外绑定其完整工作树。

## 保留的失败与修正

- 默认沙箱无 Docker 管道权限：[失败报告](early-failures/docker-permission.json)。获得运行隔离测试所需权限后执行，无复用开发数据库卷。
- 旧配置负例的测试名称直接含合成认证 URL，被秘密扫描拦截：[失败报告](early-failures/test-name-secret-scan.json)。改为固定编号名称；未放宽扫描，不保存被拦截原始输出。
- 实库集成夹具首次失败：[报告](early-failures/integration-fixtures.json)。CLI 夹具修改 pgx Config.Database 后 ConnString 仍指向旧数据库，改为显式序列化独立测试 DB；测试专用敏感路由补齐 RequestIDs 中间件。修复后完整重跑通过。该轮源码同时有变动，报告如实保持 fail。
- 静态联查发现内部数据库角色 `admin` 与公开契约 `administrator` 映射缺失，在最终实库前修复，不改公开角色契约。
- 新增 Compose 客户端最初未显式加载 PowerShell Utility 程序集，在创建任何容器前失败。加载内置模块后 31 项完整烟测通过。

## 限制

新工作树未提交或推送，因此未宣称本窗远端 CI 通过。没有人工 UI 验收；T-047 前端页面和其他业务留在后续。未升级能力 verified，复用已批准的限定 G0 证据，不重复宣称其他内核／架构兼容。

最终统一入口完整运行也通过，复核同一身份实现的全部实库检查；见 [T-008 最终报告](../T-008/validation/report.json)。
