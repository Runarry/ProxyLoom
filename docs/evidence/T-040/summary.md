# T-040 最小安全内核执行框架

状态：实现与本地检查通过，待最终评审。这是 AC-18 配置层子集与执行框架烟测，不是连通性、测速、mTLS 或 G0。

## 产物

- `internal/runner/exec`：注册表、0700 任务目录、清环境、日志限额、超时/取消、进程组回收。
- `internal/adapter/xray|singbox|mihomo`：锁定 argv（设计 §9.4）。
- `fixtures/runner/validate/` 手写回环正负配置。
- `scripts/verify-core-exec.mjs`：锁定 Debian、`--network none`、amd64 真实内核。
- Runner HTTP 仍为 idle；`Dockerfile.runner` 不含内核。

## 已执行验证

起点提交：`d22e14fa7d1175541d74fec33394fcf526e77fc7`。主机 Windows amd64，Docker Engine linux/amd64。

```text
go test -mod=readonly -count=1 ./internal/runner/exec ./internal/adapter/xray ./internal/adapter/singbox ./internal/adapter/mihomo
node scripts/verify-core-exec.mjs
```

Helper 测试覆盖：拒绝继承 `HTTP_PROXY`、拒绝 `sh` 与 `https://` 配置路径、超时、取消回收 spawn-child、64 KiB 日志截断、错误摘要拒绝。Windows 不检查 POSIX 0700 位。

`verify-core-exec.mjs` 使用 `debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171`。三家族 linux/amd64：合法配置检查 exit 0；损坏配置非 0；回环启动后 TERM/KILL，`/proc/$pid` 消失。原始 JSON 见同目录 `core-exec-report.json`。未执行 arm64 二进制。

同一 `Dockerfile.check` Linux race 含 `./internal/runner/exec`（进程组路径）。镜像清单 `sha256:6d4672388a5c3be156ac12f55f65b0cf08834fad4db2a62ef0fd75843ba4367a`。

评审修复：`Run` 先 `EvalSymlinks` 再禁 shell（含 `sh.exe`/`bash.exe`）；任务目录下 `tmp` 写入 `TMPDIR`/`TMP`/`TEMP`/`XDG_*`；`ValidateConfig`/`Start` 有 helper 入口测试；`check-boundaries.mjs` 覆盖 `internal/runner`。未改 `verify-core-exec.mjs`。

未执行：任务领取、mTLS、连通性探测、把内核装入 Runner 镜像。
