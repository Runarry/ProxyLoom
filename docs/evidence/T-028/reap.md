# T-028 子进程回收

关联 T-025／T-040。历史失败不得删除。

## 已记录失败（保留）

`docs/evidence/T-025/summary.md`：2026-09-07 局域网 Debian linux/amd64（xanmod、1 vCPU）上 `TestTimeoutAndCancelReapHelper` 在取消 `spawn-child` 后，对打印的 `child_pid` 执行 `kill(pid,0)` 仍成功。跳过该用例后其余 exec 测试通过。同一用例此前在 Docker Debian bookworm 上通过。

## 本窗口复现

在本机 Docker linux/amd64（`golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c`，`--cpus=1 --init=false`）于修复前复现失败。`/proc` 快照示例：

```text
grandchild pid=1636 snapshot=state=Z ppid=1 pgrp=1631
status=[Name:exec.test; State:Z (zombie); PPid:1]
```

连续失败中状态均为 `Z`、`PPid=1`，不是 `R`/`S` 活进程。

## 原因

`spawn-child` 再启动 `sleep` 孙进程。`Setpgid` + 进程组 SIGTERM 会杀掉 helper 与孙进程。helper 在 `Wait()` 中被杀死，未收割孙进程，孙进程成为僵尸并过继给容器 PID 1（无 tini 时 `go` 不收割）。`kill(pid,0)` 对僵尸返回成功，测试把“仍存在”判成“仍活着”。systemd 主机上 PID 1 可能稍后收割，表现为时序抖动。这不是测试误用 `kill(pid,0)` 本身，而是执行器未收割孙进程导致的僵尸。

xanmod 裸机未再接入；Docker `--cpus=1 --init=false` 是更严的 PID 1 不收割环境。

## 处理

- 不跳过 `TestTimeoutAndCancelReapHelper`。
- Linux：`PR_SET_CHILD_SUBREAPER`（`SYS_PRCTL` 36）。
- `cmd.Start` 与 pid 登记持同一把锁，避免「已 Start、未登记」窗口被 `reapOrphans` SIGKILL。
- 进行中计数 `inFlight`：大于 0 时禁止 `Wait4(-1)`。孤儿只对非 live pid 做 `Wait4(pid)`。
- 进程组终止后按 `/proc` 回收 `PPid==self` 且不是当前 `cmd` 的子进程；对非僵尸 SIGKILL 再 `Wait4`。
- 测试最多轮询 1s：超时若为 R/S/D/I 报仍在运行，否则报仍存在（含僵尸）。`TestConcurrentRunDoesNotStealWait` 覆盖两个重叠 `Run()`。

## 验证

- Windows：`go test -mod=readonly -count=1 ./internal/runner/exec` 通过。
- 修复后同一 Docker 镜像：`--cpus=1 --init=false` 全包通过；该用例 `-count=20` 20/20。
- `docker build -f deploy/Dockerfile.check` 中 `go test -race ./internal/runner/exec` 通过（约 4.9s）。

未验证：原 xanmod 主机、arm64 执行、真实内核 worker 树（夹具为 helper `spawn-child`）。
