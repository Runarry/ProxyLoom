# M2 / G2 验收证据索引

2026-09-12，基点 `15efc0e32a101f824440c1c881ab92b43a9469d7` 上的未提交工作树。本记录是用户授权的 AI 辅助工程验收，不是人工签字或远端 CI 结果。

## 最终运行

| 检查 | 结果及证据 |
| --- | --- |
| 完整质量入口 | `7027fb83-8fea-46cf-9a37-9e826d4ba49a`，13 个步骤通过，包含 Windows Go vet／race、前端构建、生成类型／SQLC 漂移、精确契约声明与秘密扫描。[报告](quality/report.json)／[源码清单](quality/source-manifest.json)。 |
| 全量 PostgreSQL | `a2615d4e-c949-4883-8ae5-b2e4720c174e`，65 项通过，0 跳过，专属容器和网络清理成功。[报告](foundation/report.json)／[源码清单](foundation/source-manifest.json)。 |
| M2 完整专项 | `778d05f7-9735-4e01-9746-5cab39f233da`，实库 8 项、真实三内核发布、五种 VMess cipher 参数矩阵、真实浏览器 3 项全部通过；输入源码稳定。[报告](m2/report.json)／[源码清单](m2/source-manifest.json)。 |
| 原生最终字节 | 真实 API Worker → 加密输出与任务 → mTLS Runner → 三个锁定 Linux/amd64 内核 → 确认发布 → 原字节下载 → 撤销。[原生输出](m2/native-publication.txt)。运行二进制摘要在报告内。 |
| 实际浏览器 | Edge、生产构建 Vue、真实 PostgreSQL／API／Worker／Runner／内核。3 条流程覆盖两次发布和历史回滚、移动剪贴板、轮换／撤销、DB 断连与恢复、412 保稿、复制、过期输入、构建停用。[浏览器输出](m2/browser.txt)／[服务端退出](m2/browser-native.txt)。 |
| Runner 回归 | 正反配置各三目标、未登记／缺失／错误 CA 证书、租约 fencing 与幂等结果通过；也再次运行 M2 原生发布。[报告](runner-regression/report.json)／[输出](runner-regression/output.txt)。 |
| Linux 工程检查 | 锁定容器构建的 `check.mjs` 和 `go test -race ./...` 通过；Windows 跳过的五项 POSIX 测试在 Linux 补验全部通过。[记录](linux-checks.json)。 |
| Compose | 重新构建开发 API／Runner，37 项通过；覆盖迁移、近期认证、角色权限、API 重启、DB 断连／恢复、Runner 边界、日志与清理。[烟测](compose-smoke.json)。 |

## 四批交付与验收映射

| 批次 | 任务与证据 | 交付结果 |
| --- | --- | --- |
| 第一批：订阅与冻结 | [T-030](../T-030/acceptance.md)、[T-031](../T-031/acceptance.md)、[T-032](../T-032/acceptance.md) | 类型化订阅、独立成员、标签语义、完整依赖、固定输入、最终检查和所需能力证据接入完成。 |
| 第二批：编译与确认 | [T-033](../T-033/acceptance.md)、[T-034](../T-034/acceptance.md)、[T-036](../T-036/acceptance.md) | 持久编译／真实校验、脱敏差异、操作者确认绑定、令牌签发与元数据重放完成。 |
| 第三批：发布与下载 | [T-035](../T-035/acceptance.md)、[T-037](../T-037/acceptance.md)、[T-050](../T-050/acceptance.md) | 原子发布、历史回滚、逐次授权、实时撤销以及浏览器完整流程完成。 |
| 第四批：G2 联验 | [T-038](acceptance.md)及以上专项 | AC-13～17、适用 AC-23 通过；[G2 决策](../../reviews/2026-09-12-g2-decision.md)记录关闭依据。 |

## AC 对照

- AC-13：实库两组订阅与复制，空 selector 不扩大成员，自动链依赖、排除冲突；浏览器创建多目标方案。
- AC-14：事务测试注入一个目标失败，整个批次失败且旧发布仍活动；真实 Runner 校验三正三负配置，失败不被当作 pass。
- AC-15：普通目录编辑使未发布批次 obsolete；不同操作者未查看预览、摘要篡改或 generation 冲突不能发布。每目标同一输入编译 100 次字节相同。
- AC-16：并发首次发布只出现一个成功 generation；并发切换读取完整三个目标；撤销提交后并发新请求没有配置字节。令牌范围、到期和管理 API 隔离通过。
- AC-17：普通改名／默认 stale 不误伤安全旧版；凭证轮换、停用／删除节点和停用构建阻断旧版与回滚。构建再次登记不能重新启用已停用记录。
- AC-23（M2 适用）：最终字段拒绝、结构化脱敏、令牌仅首次返回、敏感导出重认证、秘密扫描、模板日志、数据库故障无配置。没有把配置加载成功提升为任意远端互通证明。

恢复证据明确覆盖冻结后重建 Repository／Queue、产物入库及幂等回报、已有一个目标 pass 时重建后完成剩余目标。旧 lease 不可写入产物，重复完成不重复创建输出。此为真实数据库上的进程状态丢失模拟；Compose 另验证实际 API 重启，M3 的生产恢复演练仍未执行。

## 验证边界与变更记录

本窗口不修改锁定内核或宽泛 capability 状态。可发布范围仍要求匹配实际构建、映射／参数和行为证据，同时本批完整输出真实检查通过。当前真实链路行为证据为 Trojan TCP/TLS 两跳；其他协议链的远端互通不作无依据承诺。Linux/arm64、桌面／移动客户端导入、网络测速、生产安装／升级／备份演练仍由后续任务验收。sing-box 的已批准 DNS／IP 限制保留。

先前失败及纠正见 [迭代记录](iterations.md)。Linux 工程镜像和 Runner 回归先于最后补强的存储恢复测试；应用、编译器、Runner、迁移及契约没有后续改动，最终 Windows race 与全量 PostgreSQL 已包含该测试补强。最终门禁后仅写入验收、README／PLAN 状态与证据，不重写历史报告。最后差异与秘密扫描记录见 [收口检查](closeout-checks.json)。

M2 原 34～54 人日估算保留为规划基线；实际人日未采集。本次已完成四批范围，M2 剩余开发项为 0，不以自动化执行时间推算人工效率。
