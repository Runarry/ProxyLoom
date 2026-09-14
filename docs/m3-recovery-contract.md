# M3 加密备份与隔离恢复

采用锁定的 [age v1.3.2](https://pkg.go.dev/filippo.io/age@v1.3.2) 加密格式。备份流包含版本化清单和 PostgreSQL custom dump；清单包含备份时间、Schema 版本、主密钥 ID 及与应用密钥绑定的校验值，全部位于加密层内。日常备份只需备份公钥；age 私钥独立保管，仅在恢复时挂载。主密钥、旧主密钥、内容 HMAC 密钥及令牌密钥仍需独立保管，不写入数据库备份。

独立运维可执行文件已接入以下命令。API 可执行文件不包含外部进程执行入口，运维镜像为一次性容器：

```sh
proxyloom-operations admin create-backup-key --identity-output /keys/backup.identity --recipient-output /keys/backup.recipient
proxyloom-operations admin backup --directory /backups
proxyloom-operations admin verify-backup --input /backups/example.age --identity-file /keys/backup.identity
proxyloom-operations admin restore-backup --input /backups/example.age --identity-file /keys/backup.identity
```

命令通过已有 `PROXYLOOM_MASTER_KEY_ID`、主密钥/旧主密钥文件、内容 HMAC 文件及令牌密钥文件读取应用密钥。备份需要 `PROXYLOOM_BACKUP_RECIPIENT_FILE`；备份和恢复需要 `PROXYLOOM_MIGRATION_DSN_FILE`。PostgreSQL 客户端默认位于 `/usr/lib/postgresql/17/bin`，可通过 `PROXYLOOM_PG_TOOLS_DIR` 指定。恢复的私有加密临时副本使用 `PROXYLOOM_RESTORE_WORK_DIR`，未设置时使用系统临时目录；交付环境需为它提供足够的磁盘空间。

输出目录必须已存在且可写，路径使用绝对路径。命令不覆盖已有备份或密钥文件。数据库口令通过 [libpq 密码文件](https://www.postgresql.org/docs/17/libpq-pgpass.html) 传递，不放在子进程参数或环境值中；子进程不继承应用主密钥配置。备份完成后同步文件并原子创建目标名称，成功后记录维护时间与审计。M3 以前的兼容 Schema 也允许在升级前备份，不要求先执行迁移。

恢复先完整验证 age 文件和必需密钥，再在同一个数据库事务中检查空目标、执行 `pg_restore` 生成的 SQL、核对原迁移摘要、追加缺少的迁移并重置授权。任何最终检查失败会回滚整次恢复。拒绝恢复到非空数据库，不提供清空现有产品库的快捷参数。全程不落盘明文 dump。

恢复生成新的随机 Runner 授权纪元，删除旧会话、撤销令牌，并终结未结束任务。已经领取且用量未知的 attempt 按预留上限结算；尚未执行的预留释放。旧租约和旧 Runner 登记不能继续使用，下载不会重跑。对同一份旧备份再次恢复也会生成不同纪元。恢复后需在新登记中填写输出的 `authorization_epoch`，并按交付流程重新配置 Runner 身份，再启动常驻服务。

`docs/evidence/T-056/` 记录合成小数据集的真实 pg_dump/pg_restore、截断拒绝、最终检查回滚、非空目标拒绝及旧权限不复活。该时长是此数据集的观测，不代表容量上限或原生 arm64 性能。每日调度、完整交付脚本、规模演练及 RPO/RTO 验收仍待部署批次收口。
