# ADR-0003：M0 编译骨架与最小执行框架

日期：2026-09-07。关联 T-024、T-040。状态：采用本窗口实施选择，交付评审待定。补充设计 §6、§9、§17.1，不替换历史决策编号。

本窗口交付确定性编译 Plan 与 Runner 侧进程执行库。不宣称任何组合 `verified`，不实现三目标原生配置，不把内核打进 Runner 镜像，不开放任务 HTTP/mTLS。

1. `adapter.Compiler` 的 M0 产物是规范 Plan JSON，不是 Xray/sing-box/Mihomo 文件。T-025～027 复用 `Prepare` 再 Emit 原生 AST。
2. 发布路径必须拒绝 unverified。M0 骨架在构建已锁定且组合列入 P0 时允许 Artifact，并在 Plan 中保留 `unverified`。`unsupported` 与未知组合失败关闭。
3. `compat/cores.lock.yaml` 的 `adapter_version` 升为 `0.1.0-m0-skeleton`。能力状态仍全部 `unverified`。
4. 标签按设计 §6.3：`n_<hash>` / `c_<hash>_h1|_h2`，碰撞只延长长度。
5. 执行框架在 `internal/runner/exec`：注册表按 CoreBuild ID 解析本地二进制并核验 SHA-256；argv 由 `internal/adapter/{xray,singbox,mihomo}` 固定生成；任务目录 0700/0600；清空环境；日志 64 KiB 上限；Linux 进程组 TERM/KILL。Runner HTTP 仍为 idle。
6. 真实内核 `config_validate` 只在锁定 Debian 镜像、`--network none` 下由 `scripts/verify-core-exec.mjs` 执行。默认 `go test ./...` 使用本机 helper，不 `Skip` 内核测试当绿。二进制留在 gitignored `.cache/cores/`。

代价：Plan 不能证明客户端可导入或链路方向正确；Windows 宿主不能本地执行 linux 内核，G0 仍依赖后续 T-025～028 与 Linux 证据。升级内核必须新增 CoreBuild。
