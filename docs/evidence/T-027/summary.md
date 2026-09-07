# T-027 Mihomo 纵向原型

状态：实现与锁定内核检查通过，待最终评审。这是 AC-07／AC-09 的 Mihomo 子集，不是真实链路、客户端导入或 G0。

## 产物

- `internal/adapter/mihomo/emit.go`：完整 Mihomo YAML（回环 mixed-port、独立链实例、`dialer-proxy`、`MATCH,<exit>`）。
- 禁用 `geodata-mode`／`geo-auto-update`，清空 `external-controller`，`mode: rule` 且规则不指向 `DIRECT`。
- Golden：`fixtures/compiler/golden/mihomo-native-chain-a-b.yaml`。
- `adapter_version`：`0.1.0-m0-native`。能力仍全部 `unverified`。

## 前置检查

起点提交：`19c7035a9ae5e320b9ece05cef6706d5c13b5433`。实现位于该提交之上的工作树。主机 Windows amd64，Docker Engine linux/amd64 29.7.2。

锁定 linux/amd64 二进制 SHA-256 与锁文件一致：

- mihomo `v1.19.30` `81407b9e-61c5-5473-b359-29fa2730567e`：`3e92df24f5e80e86b9cf9183ceb7bb575f0bd132a9dc4081dae42e80f21076ae`

T-053 证书已补齐，Compose 配置可解析；隔离容器烟测未启动。本轮不连接夹具。生成配置保持 `skip-cert-verify: false`。

## 已执行验证

```text
go test -mod=readonly -count=1 ./internal/compiler ./internal/adapter/mihomo
node scripts/verify-compile-exec.mjs
```

手写配置回归通过后，锁定 Debian、`--network none` 接受编译器生成 YAML，拒绝把 `type: trojan` 改成非法值的副本，并回收回环启动。报告见 `compile-exec-report.json`。

单测覆盖：h2 `dialer-proxy` 指向 h1、独立节点无该字段、同名节点不串用标签、`MATCH,DIRECT` 与 `mode: direct` 被拒绝、隐式下载关闭、连续 100 次字节一致。链失败不会选择隐含直连。

同一 `Dockerfile.check` Linux race 运行包含 `./internal/adapter/mihomo` 与 `./internal/compiler`。镜像清单 `sha256:d2e122c873faffbcd64f651e3d63f6ae8017d20ce2b71a3b2be7d9119dd9951d`。

Linux 主机补测见 `docs/evidence/T-025/summary.md`：同一 Debian linux/amd64 上 `go test ./internal/adapter/mihomo ./internal/compiler ./internal/isolation` 通过，锁定 mihomo 对本轮生成配置正负检查与回环启动回收通过。未跑隔离 Compose，未做真实链路。

未执行：arm64、真实 A→B 拨号、客户端导入、`verified`、T-028／G0。原生配置检查通过不等于链路验证通过。
