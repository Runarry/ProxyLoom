# T-024 公共编译骨架与标签

状态：实现与本地检查通过，待最终评审。这是 NFR-04 Golden 原型，不是三内核原生配置、发布编译或 G0。

## 产物

- `internal/compiler`：`Prepare` / `Compile`、确定性标签、规范 Plan JSON。
- `docs/compiler-contract.md`、`docs/adr/0003-compile-and-exec-skeleton.md`。
- `fixtures/compiler/frozen-chain-a-b.json` 与 `fixtures/compiler/golden/xray-chain-a-b.json`（无节点秘密）。
- `compat/cores.lock.yaml` `adapter_version`：`0.1.0-m0-skeleton`。P0 组合仍全部 `unverified`。

## 已执行验证

起点提交：`d22e14fa7d1175541d74fec33394fcf526e77fc7`。主机 Windows amd64，Go 1.26.0。实现位于该提交之上的工作树。

```text
go test -mod=readonly -count=1 ./internal/compiler
```

实测：Trojan A→B 三目标 Compile 成功；Plan 记录 `capability_state: unverified`；链出口为 `c_*_h2` 且 `dialer_tag` 为 h1；改名不改字节；连续 100 次字节一致；标签跨家族相同；假 build / 示例 pin / SOCKS5+TLS 被拒；Golden 与 `xray-chain-a-b.json` 一致。日志不出现 `EXAMPLE_ONLY_*`。

`node scripts/check.mjs` 在 Windows amd64 通过（含 `go test -race ./...`）。`docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-t024-t040 .` 在 linux/amd64 通过，镜像清单 `sha256:6d4672388a5c3be156ac12f55f65b0cf08834fad4db2a62ef0fd75843ba4367a`。

评审修复：`0.1.0-m0-skeleton` 不再把 Plan 写成 `verified`；`docs/ir-contract.md` 与 `adapter.Compiler` 注释改为「未知/`unsupported` 失败，骨架可记录 unverified，发布另拒」。

未执行：Xray/sing-box/Mihomo 原生 Emit（T-025～027）、真实链路、将能力标为 `verified`。
