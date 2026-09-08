# T-004：资源修订与引用存储

日期：2026-09-08。状态：实现与自动验收通过，`in_review`。G0 批准见 [记录](../../reviews/2026-09-08-g0-decision.md)，本报告不代替本窗人工评审或完整 G1。

## 交付

- 追加迁移 000002／000003，保留 000001 原字节；提供 scopes、资源头、不可变完整修订、引用、当前标签、独立 DEK 包装和持久幂等回执。
- catalog 领域不依赖数据库，storage 采用锁定 sqlc 1.31.1 的 pgx/v5 查询；Node／Chain 的 ID、修订和 epoch 由服务端生成，旧修订保留原名称、标签及载荷。
- 写入先锁 scope，再按 UUID 锁相关资源；同事务多次业务变更只推进一次目录版本，过期更新、失败回调、吞掉的变更错误均不能部分提交。软删除保留历史与引用；认证轮换、停用和撤销推进安全 epoch。
- 数据库约束覆盖无头提交、错 scope／kind／固定修订、不可变历史、标签必须伴随修订及最小运行权限。重包使用独立 wrap_version CAS，不改业务密文、HMAC、修订或 epoch。
- 幂等领取、业务变更与安全回执同事务提交；并发相同请求仅一份效果，异请求冲突，重放不返回一次性原始秘密。不提供任意响应字节持久化入口。

## 最终证据与执行范围

基点提交：`fdc7fc3956e6892ffc68d1873d2d7d2d70edaaf0`；实现位于该提交之上的本窗工作树。

[源码清单](validation/source-manifest.json) 包含 253 个文件；清单 SHA-256 为 `21d8693b97fc05d50f98d3d63e2ac40ee0411c63e397e4629190e0789224376c`。涵盖源码、测试、契约、生成类型、工具锁、构建输入与测试读取的两份基线；不包含证据目录和本地秘密。最终 PostgreSQL 运行期间逐项核对未发生变化。

| 验证 | 结果与记录 |
| --- | --- |
| `node scripts/verify-foundation.mjs` | PASS；Go 1.26.0 windows/amd64 测试进程连接独立 PostgreSQL 17.6 linux/amd64；9 组数据库测试及子项无 skip；[报告](validation/report.json)、[Go 事件](validation/go-test.jsonl) |
| 新库／bootstrap 前缀／重复迁移／失败回滚 | PASS；迁移以独立 migrator 执行，运行账号不能建 schema |
| 数据库约束、权限及引用负例 | PASS；含无头提交、历史改写／追加／删除／TRUNCATE、跨 scope／kind／不存在的固定修订、元数据绕过和 epoch 回退 |
| 生命周期、事务与分页 | PASS；历史不污染，同事务目录 +1，并发旧修订仅一方成功，失败 latch、Tx 逃逸和 scope 隔离有效 |
| 并发幂等与重包 | PASS；6 个同请求只写一次，持久重放、异体／跨 scope 冲突、无孤儿 key、包装 CAS 竞态和旧密钥退役后读取有验证 |
| API → 加密存储集成与秘密扫描 | PASS；三态秘密合并、脱敏读取、修订／epoch／目录计数，以及原始 bytea 与元数据扫描；由独立验收用例覆盖 |
| `node scripts/check.mjs`（Windows） | PASS；含依赖／任务／边界／sqlc 检查、格式、vet、全包 race；[日志](validation/engineering.txt) |
| `docker build -f deploy/Dockerfile.check -t proxyloom-check:m1-foundation-01a07f6e .` | PASS；Linux 检查层实际执行 75.7 秒，含全包 race；[日志](validation/linux-check.txt) |
| API 生成漂移、前端 typecheck／build | PASS；锁定 openapi-typescript 7.9.1，最终生成类型 SHA-256 见 T-007 证据；Compose `-Build` 同时完成前端构建 |
| `pwsh -NoProfile -File scripts/smoke.ps1 -Build` | PASS；21 项检查，重复／并发迁移、缺钥、数据库故障恢复、保留路由和日志检查通过；[报告](validation/compose-smoke-report.json)、[日志](validation/compose-smoke.txt) |
| 测试环境清理 | PASS；数据库 harness 按标签清理；Compose 额外核对容器、网络和卷均无残留；[核对](validation/compose-cleanup.json) |

Linux 检查镜像摘要：`sha256:7112fdc34830d2aaed563952494b42c79be414db477def0fc1f7354a85f84d9d`。普通 Go 检查中的数据库用例缺少专用 DSN 时会明确跳过，其结果不替代上述真实 PostgreSQL 验收。`.gitattributes` 最后固定生成 TS 为 LF，并通过属性与生成检查；该元数据调整不改变先前 Windows／Linux 检查的应用或测试源码，最终数据库清单已包含它。

## 保留的过程问题与边界

- 初轮 Docker Desktop internal 网络未分配宿主回环端口，脚本失败并清理。随后使用独立 bridge，并强制核验发布地址为 127.0.0.1；[原失败](validation/initial-port-failure.json)。该测试网络不代表生产 Runner 的出站隔离。
- 契约尚未落齐时跨模块用例失败，四组基础数据库测试已通过；待契约完整后重新验收全部通过，[原记录](validation/initial-contract-incomplete.json)。修复测试 helper 的环境变量名称，并在 harness 中强制验证数据库用例实际执行。
- bytea 扫描从 JSON 的十六进制显示改为读取原始字节，改进后完整重跑通过；未把不足的扫描作为最终秘密验收依据。
- 本机再次运行开发密钥初始化时受已有目录 ACL 限制，[失败日志](validation/dev-init.txt)。核对已有八个开发秘密文件完整并保留值后，独立 Compose 烟测成功；本窗不声称在该 Windows 环境重新生成了开发密钥。
- Source provenance、公开节点 CRUD、认证、发布、持久 Runner 协议不在本窗实现范围。未重跑真实三内核链路，未更改 IR／编译器／适配器／核心锁；复用已批准的限定 G0 证据，能力继续 unverified。
