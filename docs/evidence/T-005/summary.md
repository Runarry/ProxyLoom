# T-005：秘密、摘要与脱敏基座

日期：2026-09-08。状态：实现及自动验收通过，`in_review`；不是完整 AC-23 或 G1 的验收声明。

## 交付

`internal/secretbox` 提供 Seal、Open、Rewrap 与用途隔离 HMAC。AES-256-GCM 使用独立随机 DEK／nonce；上下文绑定 scope、表、对象、修订及 Schema 版本，包装另外绑定 key_id 与业务载荷摘要。错钥、失钥、篡改、未知版本或上下文交换失败关闭。业务载荷与 DEK 包装分开持久化，重包不变更内容摘要和资源安全版本。

`internal/config` 保留三份独立原始 32 字节密钥文件，新增必填 `PROXYLOOM_MASTER_KEY_ID` 和可选 `PROXYLOOM_OLD_MASTER_KEYS_FILE`。旧密钥配置为 key_id 到绝对文件路径的严格有界 JSON 对象，最多 15 个旧密钥；不生成替代密钥、不复用不同用途原始值。ReadKeys 返回可 Clear 的独立缓冲区，键材料及秘密聚合默认 fmt／slog 脱敏。

管理 API 的秘密三态补丁和脱敏 DTO 由 T-007 实现：缺省保留、null 清除、新值替换，完成合并后校验协议；必需凭证被清除或写入掩码返回 422。包括密码、UUID、用户名及被 IR 视为秘密的 REALITY 字段，响应不复用完整 IR JSON。

## 验证

- `go test`、`go vet`、`go test -race ./internal/secretbox ./internal/config` 在 Windows/amd64 通过；最终 Windows／Linux 全工程检查均通过。
- 单测覆盖每个 AAD 字段、逐字节篡改／截断、错钥／失钥、格式版本、随机性、HMAC 稳定性、输入／返回缓冲区所有权、重包完整认证、配置边界与默认日志脱敏。
- 最终真实 PostgreSQL 验收包含重包 CAS、轮换后读取、原始密文／HMAC／修订／epoch 不变，以及 API 秘密合并后的加密存储与原始 bytea 扫描。
- 共同源码清单、真实 PostgreSQL 报告、Windows／Linux race 与 Compose 日志归档于 [T-004 验证](../T-004/summary.md)。最终 253 文件清单摘要：`21d8693b97fc05d50f98d3d63e2ac40ee0411c63e397e4629190e0789224376c`。

`Wrapping.Version=1` 是加密格式版本，数据库独立 `wrap_version` 才是并发轮换计数。移除旧密钥须先确认所有仍需访问的包装已迁移；Go 内存清除为尽力而为，不承诺清除所有运行时拷贝。管理员重认证、秘密导出审计和完整恢复流程仍由后续任务实现。
