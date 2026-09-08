# M1 跨模块独立验收记录

日期：2026-09-08。编写：集成主代理依据独立验收代理的用例、复核消息和实际执行报告归档；不代替人工项目评审。

独立验收代理新增 `internal/storage/foundation_acceptance_test.go`，与实现任务的迁移、领域和密码学测试分开负责。用例覆盖严格 API 解码 → 类型化节点创建 → 同事务幂等回执及重放 → 加密资源读取 → 秘密缺省／替换／清除 → 脱敏响应，以及修订、security_epoch、catalog_revision 的变化。

| 范围 | 结论 | 依据 |
| --- | --- | --- |
| API 与 Go Schema／DTO 边界 | PASS | 独立复核 `go test -mod=readonly -count=1 ./internal/apicontract ./api`，在完整契约落盘后通过 |
| 独立 API → PostgreSQL 用例 | PASS | 最终 [Go 事件](validation/go-test.jsonl) 中 `TestPostgresFoundationAPITransactionBoundary` 实际通过 |
| 完整数据库联验 | PASS | [报告](validation/report.json)：9 组 TestPostgres 用例通过，所有子项无 skip，源码运行期间稳定 |
| 明文扫描 | PASS | 最终版本直接扫描原始 bytea 与 UTF-8 元数据，不使用会掩盖字节内容的 JSON 十六进制显示 |
| 测试容器／网络清理 | PASS | 数据库报告 cleanup 全 pass；Compose [单独核对](validation/compose-cleanup.json) 包含容器、网络、卷 |
| Windows／Linux 工程与 Compose | 主代理验证 PASS | 完整范围及日志见 [主报告](summary.md)，不冒称独立代理重复执行 |
| 真实三内核、arm64、登录及发布 | NOT RUN | 非本窗实现范围，不能由本验收推断通过 |

独立复核曾发现／确认测试 helper 的 DSN 环境名称不一致，以及契约缺少 components 时的 INTERNAL_ERROR；均在最终验收前处理。第一次跨模块数据库失败保留于 [过程记录](validation/initial-contract-incomplete.json)，没有将当时的跳过或失败计为通过。

最终基点为 `fdc7fc3956e6892ffc68d1873d2d7d2d70edaaf0` 上的本窗工作树，[253 文件源码清单](validation/source-manifest.json) SHA-256 为 `21d8693b97fc05d50f98d3d63e2ac40ee0411c63e397e4629190e0789224376c`。最终仅追加生成 TS 的 LF 属性以保证跨平台生成检查，并再次运行完整数据库验收；没有在验收后修改应用或测试源码。

结论：本窗自动技术验收满足 T-004／T-005／T-007 的已实施范围，任务进入 in_review；T-006／T-008 和完整 G1 保留原后续门槛。
