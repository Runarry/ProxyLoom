# T-025 Xray 纵向原型

状态：实现与锁定内核检查通过，待最终评审。这是 AC-07／AC-09 的 Xray 子集，不是真实链路、客户端导入或 G0。

## 产物

- `internal/adapter/emit.go`：公共 Emit 输入、Trojan TCP/TLS 映射与失败关闭诊断。
- `internal/adapter/xray/emit.go`：完整 Xray JSON（回环 SOCKS、出站、`dialerProxy`、指向末跳的路由）。
- `internal/compiler`：`Compile` 复用 `Prepare` 后调用家族 Emit。
- `compat/cores.lock.yaml` `adapter_version`：`0.1.0-m0-native`。
- Golden：`fixtures/compiler/golden/xray-native-chain-a-b.json`（合成夹具密码 `EXAMPLE_ONLY_*`）。

## 前置检查

起点提交：`19c7035a9ae5e320b9ece05cef6706d5c13b5433`。实现位于该提交之上的工作树。主机 Windows amd64，Docker Engine linux/amd64 29.7.2。

锁定 linux/amd64 二进制 SHA-256 与 `compat/cores.lock.yaml` 一致：

- Xray `v26.3.27` `42f06ab1-ac85-524b-87e5-6bc1e9a82409`：`8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed`

T-053：`.cache/isolation/certs` 原先缺失，本窗口执行 `go run ./proxyloom-fixtures init-certs` 生成共享测试 CA 与 A/B 叶证书；`docker compose -f deploy/compose.isolation.yaml config --quiet` 通过。隔离容器烟测仍未启动。本轮最小运行是对 `a.example.invalid`／`b.example.invalid` 生成配置做内核检查与回环启动，不连接隔离夹具；生成配置保持 `allowInsecure: false`，未关闭测试 CA 校验。

## 已执行验证

```text
go test -mod=readonly -count=1 ./internal/compiler ./internal/adapter ./internal/adapter/xray
node scripts/verify-compile-exec.mjs
```

`verify-compile-exec.mjs` 先回归 `scripts/verify-core-exec.mjs` 手写配置，再把本轮编译 Golden 交给锁定 Debian `debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171`、`--network none`。Xray 合法生成配置检查通过；故意损坏协议后被拒绝；回环启动后 TERM/KILL，`/proc/$pid` 消失。原始 JSON 见 `compile-exec-report.json`。

单测覆盖：字段映射、A→B 的 `dialerProxy`、独立节点无拨号、原冻结输入不变、同名节点标签隔离、混合成员路由指向 h2、P0 Shadowsocks／`udp=true` 拒绝、连续 100 次字节一致。日志不出现 `EXAMPLE_ONLY_*`。

`docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-t025-t027 .` 在 linux/amd64 通过，含格式、锁与计划一致性、Go 静态分析及 `go test -race ./...`。镜像清单 `sha256:d2e122c873faffbcd64f651e3d63f6ae8017d20ce2b71a3b2be7d9119dd9951d`。

## Linux 主机补测（ssh host-vps-scripts）

2026-09-07 在局域网 Debian linux/amd64（xanmod 内核、1 vCPU）上用锁定官方二进制复测，不使用该机已有的 `/usr/bin/xray`／`sing-box`。工作区位于 `/var/tmp/proxyloom-m0-t025`，测完已删除。

- `go test -count=1`：`internal/compiler`、`internal/adapter{,/xray,/singbox,/mihomo}`、`internal/isolation`、`proxyloom-fixtures`、`internal/capability` 通过。
- 锁定内核对本轮生成 Golden 做合法检查、损坏拒绝、回环启动回收：xray／sing-box／mihomo 均通过。
- `internal/runner/exec` 的 `TestTimeoutAndCancelReapHelper` 在该内核上复现失败（取消后子进程 `kill(pid,0)` 仍成功）；跳过该用例后其余 exec 测试通过。Docker Debian bookworm 上该用例此前已通过。不据此改 T-040，也不把该主机标为执行框架已验收。

未执行：arm64 二进制、隔离 Compose 容器烟测、隔离网络真实拨号、客户端导入、将能力标为 `verified`、T-028／G0。原生配置检查通过不等于链路验证通过。
