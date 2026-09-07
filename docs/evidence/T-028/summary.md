# T-028 真实链路与不绕行验证

状态：实现与 linux/amd64 夹具链路检查通过，待最终评审。这是 AC-07～AC-10 的 M0 原型证据，不是客户端导入、arm64、其它协议或 G0 正式通过。能力保持 `unverified`。

## 产物

- `internal/chainverify`：冻结夹具地址 → `Compile` → 同一字节 `ValidateConfig`/`Run` → SOCKS 探测 `/probe`。
- `scripts/verify-live-chain.mjs`：锁定 Debian/Go 镜像、`--network none`、系统信任测试 CA、三内核矩阵。
- `scripts/verify-isolation-compose.mjs`：T-053 后置容器烟测（共享 CA、挂载、来源限制、停 A）。
- `internal/isolation`：事件 HTTP、`accept` 记录、请求关联、目标来源。
- `internal/runner/exec`：Linux 子进程组回收（subreaper + Wait4）；不跳过 `TestTimeoutAndCancelReapHelper`。
- ADR-0005：测试环境 CA 信任，不关闭校验、不给 Emit 增加自定义 CA 字段。

## 前置检查

起点提交：`c25e498312f79d554b3e7d4f2ea7fbf4a780465a`。实现位于该提交之上的工作树。主机 Windows amd64，Docker Engine linux/amd64 29.7.2，Compose v5.5.1。

锁定 linux/amd64 二进制 SHA-256 与 `compat/cores.lock.yaml` 一致：

- Xray `v26.3.27` `42f06ab1-ac85-524b-87e5-6bc1e9a82409`：`8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed`
- sing-box `v1.14.0` `ebdd7efd-a425-5eb7-a63b-88667909c4b0`：`57b3da14e264b6e05e8f46aee027c02d7dd7f1594d19aa39e2f4d2b9459bbd04`
- mihomo `v1.19.30` `81407b9e-61c5-5473-b359-29fa2730567e`：`3e92df24f5e80e86b9cf9183ceb7bb575f0bd132a9dc4081dae42e80f21076ae`

`adapter_version`：`0.1.0-m0-native`。夹具集：`isolation-v1`。Schema：IR v1。

测试 CA 只写入本轮容器 `/usr/local/share/ca-certificates/proxyloom-livechain.crt` 并 `update-ca-certificates`；不改宿主信任、开发 Compose 或数据库卷。生成配置保持证书校验开启。

## 已执行验证

```text
go test -mod=readonly -count=1 ./internal/isolation ./internal/chainverify ./proxyloom-fixtures
node scripts/verify-isolation-compose.mjs
node scripts/verify-live-chain.mjs
node scripts/verify-compile-exec.mjs
```

Windows 单测覆盖冻结 IR 编译（校验开启、标签隔离、原节点不变）及隔离观察。Live 矩阵在 `golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c`、`--network none` 中执行。场景结果见 `matrix.md` 与 `live-chain-report.json`。

Compose 烟测项目 `proxyloom-isolation-t028`，内部网 `172.30.253.0/24`；测完 `down --remove-orphans`。报告 `compose-smoke-report.json`。

`verify-compile-exec.mjs` 手写配置回归与生成 Golden 正负检查、回环启动回收通过。

`docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-t028-fix .` 通过（格式、锁、PLAN、边界、`go vet`、`go test -race ./...`，含 `internal/runner/exec` 与 `internal/chainverify`）。镜像清单 `sha256:e67202a3adf0fe923c300b984e732228b730ef01bc026efcdea45184e7c73fa1`。

首次 Mihomo `node_reuse` 因上一轮 `mixed-port` 17803 未释放而连到残留监听。评审后 `waitPortClosed` 超时改为失败，启动前须连续拒绝 Dial，就绪判定含 SOCKS 握手。故障关闭观察改为 `TargetLog.Seq()` 基线，故障后禁止新的目标 `accept`/`ok`，停 A 后禁止额外 B `ok`。执行器 `Start` 与 pid 登记同锁，`inFlight>0` 时不做 `Wait4(-1)`。未手改原生链依赖。

## 子进程回收

T-025 在局域网 Debian xanmod 上记录 `TestTimeoutAndCancelReapHelper` 于取消后 `kill(pid,0)` 仍成功。本窗口在 Docker `--cpus=1 --init=false` 下复现为**僵尸**（`State:Z`，`PPid:1`），不是仍在执行的孙进程。执行器增加 subreaper 与 Wait4 回收；用例未跳过。xanmod 裸机未复测。详见 `reap.md`。

## 未执行

arm64、T-029 完整 P0 映射、T-042 生产连通性、客户端导入、将能力标为 `verified`、G0 人工签字。linux/amd64 Trojan TCP/TLS 夹具结果不得推广到其它架构或协议。
