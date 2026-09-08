# Linux foundation CI 修复（2026-09-08）

基点提交：`6385d43b01e0c5befd5894e141d728dd1c471bc4`；修复位于其上的工作树。远端失败运行：[34196640122](https://github.com/Runarry/ProxyLoom/actions/runs/34196640122)。原 CI 未上传 foundation 附件，无法恢复其 Go 事件；以下为本地对比复现，未声称远端已重跑。

## 原因与修复

验收脚本将宿主 0700 秘密目录整体挂载，内部文件为 0600。Linux PostgreSQL 降权后无法读取运行／迁移账号密码。初始化脚本的 `export VAR="$(cat ...)"` 又以 export 的成功状态掩盖 cat 失败，导致空密码账号仍被创建；仅检查账号数量的 readiness 能通过，随后连接测试失败。Windows Docker Desktop 的文件挂载行为没有覆盖这一 POSIX 权限问题。

改为只读挂载三份独立密码文件，POSIX 文件设为 0444、宿主父目录保留 0700；DSN 保持 0600 且不挂载。初始化先独立赋值，读取失败立即退出，再拒绝空值并 export。数据库迁移与业务代码没有变更。

失败时输出有长度上限、凭据脱敏及控制字符处理的 Go 摘要。CI 使用锁定 upload-artifact，仅上传 report、source-manifest、Go JSONL 和 stderr，保留七天；不上传 secrets 目录。原始 Go 输出仍先通过生成秘密检查再保存。

## 对比验证

| 验证 | 结果 |
| --- | --- |
| 原提交，Linux Go + 原生 Linux 文件权限 + PostgreSQL | 9 组数据库测试全部连接失败；[报告](linux-before/report.json)、[事件](linux-before/go-test.jsonl) |
| 修复后，相同 Linux 环境 | 9 组全部通过，无跳过；[报告](linux-after/report.json)、[事件](linux-after/go-test.jsonl) |
| 修复后，Windows 原始 `node scripts/verify-foundation.mjs` | 9 组全部通过，无跳过；[报告](windows-after/report.json) |
| Linux `node --test scripts/foundation-report.test.mjs scripts/postgres-init.test.mjs` | 11 项通过，无跳过；[日志](linux-node-tests.txt) |
| shell 负例针对旧脚本 | 不可读／空值各两个用例均失败，因旧脚本错误返回成功；有效输入用例通过。修复后五项全部通过 |
| PLAN、依赖边界、工具锁、Node 语法、diff 空白检查 | 通过。边界检查首次因默认 Go 缓存目录不可写失败，改用仓库 `.cache` 后通过 |

锁定 Go 1.26.0、Node 24.6.0、PostgreSQL 17.6；镜像摘要见报告。Linux 工具镜像沿用 `proxyloom-check:m1-foundation-01a07f6e`，ID `sha256:7112fdc34830d2aaed563952494b42c79be414db477def0fc1f7354a85f84d9d`。

本地 Docker Desktop 的嵌套 Linux 客户端不能使用宿主发布的回环端口；最初两次嵌套 host-network 运行均连接失败，不作为权限修复对比证据。最终对比仅在隔离副本中加入[传输适配](transport-adaptation.mjs)：客户端连接当次独立 bridge，DSN 使用容器 DNS 与 5432，退出前断开客户端。前后两次使用同一适配，保留生产验收脚本的秘密权限、挂载与初始化逻辑；[实际执行脚本](linux-after/verifier-with-transport-adaptation.mjs)与每次 source-manifest 可核对。副本由 `git archive HEAD` 创建，故报告内 source_commit 为空，以本页基点和源码清单追溯。Windows 运行没有该适配。

本次修补验收环境与诊断，不涉及前端、编译器或内核；未重跑前端构建及真实内核链路。T-004／005／007 保持 `in_review`。之前 Linux 全包检查在缺少 DSN 时跳过数据库用例，其结果不等同于本次 Linux 实库验收。

收尾核对：Windows 最终清单的 256 个文件哈希与当前工作树一致；导出证据对 Linux／Windows 本次生成凭据的逐值扫描通过。各验收容器和网络已清理，另按名称及所有权标签删除本次复现卷和一次客户端网络配置失败遗留的空网络；最终 foundation 容器／网络清单为空。未提交或推送修复。
