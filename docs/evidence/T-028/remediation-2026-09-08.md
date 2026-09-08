# M0 四项修复与补验（2026-09-08）

关联 T-023、T-040、T-053、T-028；历史问题见 `docs/reviews/2026-09-08-m0-review.md`，方案见 ADR-0006。基点提交为 `dfc0ced205a09d1b99df9ec5f362820f6ee5e43f`，本轮修改在该提交之上的工作树。四项修复及所需回归已通过，交付状态保持 `in_review`。此报告保留旧失败记录，不替代 G0 人工评审。

## 修复范围

| Review | 修复 | 验收重点 |
| --- | --- | --- |
| R1 / T-040、T-028 | 每个 Run 独立进程组；根等待与日志读取分开；只收割本组；清理有界且传播错误 | 其他活动任务的被收养后代存活、非 Runner 子进程不被 signal/wait、根先退出及后代持日志句柄、TERM/KILL 与超时取消 |
| R2 / T-023 | Build/Record/EvidenceRef 全部嵌套切片深拷贝 | 修改访问器返回值不改变原目录、其他返回值和后续 Load；并发回归 |
| R3 / T-028 | 幂等清理、失败路径也清理、清理完成后记录最终测试状态 | 注入执行/目录/监听错误必须使子测试进程失败并记录 fail；预期取消不吞额外错误 |
| R4 / T-053、T-028 | 唯一项目与产物目录、全状态库存和子网检查、清理后才发布通过报告 | 已停止/孤儿资源、网络/卷、查询失败、部分 up 失败、down 失败、双异常均失败关闭 |

公开执行接口、IR、Schema、数据库、内核锁及原生编译字节保持兼容。`adapter_version=0.1.0-m0-native`，所有能力保持 `unverified`。

## 验证记录

最终整体验证结果已归档到同目录 `remediation-2026-09-08/`，命令与结果汇总见 `checks.json`。

| 检查 | 实测 |
| --- | --- |
| 能力目录单测、compiler 回归、vet 与 capability race | 通过；访问器和新 Load 的隔离以及并发修改副本均有回归 |
| 执行器针对性测试 | Linux 普通测试 12.626 秒、最终源码 Linux race 28.518 秒、Windows 授权环境测试 10.133 秒通过 |
| 生命周期负例 | 幂等/并发停止和错误合并通过；8 组子进程测试验证取消/超时正控制、清理错误、意外超时、Fatal 与非 Fatal 错误的退出码及报告 |
| `node --test scripts/isolation-compose.test.mjs` | 19/19 通过；不对真实 Docker 资源执行故障注入 |
| `pnpm --dir proxyloom-web build` | typecheck 与 Vite 构建通过 |
| `node scripts/check.mjs` | Windows 完整工程检查通过（部分原有包命中缓存）；日志 `engineering.txt` |
| `docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-remediation-20260908 .` | Linux 工程检查和全包 race 通过，检查层本轮实际执行 |
| `docker run --rm --network none proxyloom-check:m0-remediation-20260908 go test -mod=readonly -race -count=1 ./...` | 显式无缓存全包回归通过；exec 28.676 秒、chainverify 8.791 秒；日志 `linux-race.txt` |
| `node scripts/verify-compile-exec.mjs` | 三内核手写及生成配置的合法接受、损坏拒绝、启动回收均通过 |
| `node scripts/verify-isolation-compose.mjs` | startup、bypass、after-stop-a 通过，清理成功后才报告 PASS；独立项目及实际网络见 `compose-smoke-report.json` |
| `node scripts/verify-live-chain.mjs` | 三内核共 24 场景通过，矩阵 160.86 秒；51 个 session 清理均 pass，9 个故障阶段目标 accept/ok 增量均为 0 |

三内核正向请求实际出口均为 B（`127.0.2.1`）；每次 probe 的请求 ID 与返回 ID、实际出口、测试配置摘要、故障事件差值及清理结果见新的 `live-chain-report.json`，完整执行日志见 `live-chain-stdout.txt`。这里的配置 SHA-256 只标识合成测试配置，不代替生产配置所需内容 HMAC。

本次 Compose 项目为 `proxyloom-isolation-t028-f5fda4df-b5e0-4f30-98ff-24fb27f27f35`，使用原固定测试子网；未复用旧 `proxyloom-isolation-t028` 项目。

### 源码与构建绑定

`source-manifest.json` 记录本轮实际源码、测试、夹具、依赖锁和构建输入共 170 项文件 SHA-256；文档不参与该清单以避免证据自引用。清单文件 SHA-256：`955e15a3502bbb885834532272ed38bf49672d2267ea233f3065313dc4117936`。整合检查期间逐项复核一致。

Linux 检查镜像清单：`sha256:9c421f0c02be929a6cf60fa201233322a74d457d8427c6927cdae5d5d445ff3a`。三内核构建 ID/二进制摘要随场景保存在新 JSON 中。`git diff --exit-code -- compat schemas fixtures/compiler go.mod go.sum` 通过，确认锁、Schema 和 Golden 未被修改。

### 本轮过程中的失败与修复

Windows 最初在默认沙箱内无法调用 taskkill；授权测试随后暴露根等待后句柄已释放、重复 `Process.Kill` 返回 EINVAL 的竞态。修复为 taskkill 成功后不重复 Kill，并处理已释放句柄的结束竞态；最终 Windows 测试及完整工程检查通过。未把沙箱权限失败或早期执行失败写成通过。

历史 Linux 全局回收误杀及旧生命周期故障注入假通过记录保留在 review；本轮回归断言已反转为正确行为：其他活动任务后代必须存活，清理失败必须让测试进程和报告失败。

## 明确边界

Linux 组回收针对固定可信内核；主动脱离启动进程组的程序不在本组清理承诺内，不通过扫描所有子进程弥补。Windows 保留既有 taskkill 树终止机制，不新增 Linux subreaper 的等价能力。测试 CA 只安装在临时 Linux 容器中，不修改宿主信任，不关闭 TLS 校验。

arm64、历史 xanmod 裸机、其他协议/传输/客户端导入、M1～M3 生产业务未在本轮执行。相关任务保持 `in_review`，不自动 done 或宣布 G0 正式通过。
