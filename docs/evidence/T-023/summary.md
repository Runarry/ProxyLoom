# T-023 内核候选锁与能力证据

状态：实现与本地检查通过，待最终评审。全部能力保持 `unverified`。不是 NFR-13 两架构执行验收，不是 G0。

## 锁定结果

官方非预发布 linux 候选（GitHub Latest，2026-09-07 核对）：

| 家族 | 版本 / tag | 提交 | amd64 二进制 SHA-256 | arm64 二进制 SHA-256 |
| --- | --- | --- | --- | --- |
| xray | 26.3.27 / v26.3.27 | d2758a023cd7f4174a5a5fa4ff66e487d4342ba0 | 8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed | c2d20a7045250497083afea0d79db0672f6c89a25aaaf37c92de034d6b764b04 |
| sing-box | 1.14.0 / v1.14.0 | 0b8995879f29a9b98ee027bc17b75e101445b238 | 57b3da14e264b6e05e8f46aee027c02d7dd7f1594d19aa39e2f4d2b9459bbd04 | 4393306b90bb05502fce3b8f1754280f531c0f3ff47df9b1997b7831e3e543d0 |
| mihomo | 1.19.30 / v1.19.30 | ac017cdd246ce8bd547653d927e7bf77d7ee73d5 | 3e92df24f5e80e86b9cf9183ceb7bb575f0bd132a9dc4081dae42e80f21076ae | b9456718a8955364b9a77c80f74dca49ded10f071c1c6b4513a0ea68a3d87a50 |

归档摘要与 GitHub 资产 `digest` 一致，并经本地下载复算。构建 ID 为 UUID v5，见 `compat/cores.lock.yaml`。

## 已执行验证

起点提交：`93295278b3e2f66a13cd326d89071b2ddebbe7ec`。主机 Windows amd64，Go 1.26.0，Node 24.6.0，Docker Engine 可用。时间 `2026-09-07T13:00:24+08:00`。

```text
node scripts/pin-cores.mjs
go test -count=1 ./internal/capability
node scripts/verify-cores.mjs
```

amd64 版本烟测在锁定运行镜像 `debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171`（inspect Id 同 digest）中执行：

```text
Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)
sing-box version 1.14.0
Environment: go1.26.7 linux/amd64
Revision: 0b8995879f29a9b98ee027bc17b75e101445b238
CGO: disabled
Mihomo Meta v1.19.30 linux amd64 with go1.26.6 Sun Aug 16 09:58:25 UTC 2026
Use tags: with_gvisor
```

版本字符串与锁中的 tag/commit 一致。未执行 arm64 二进制，未跑配置检查或网络。二进制未提交。

原始负例：`latest` URL、客户端 `verified` 状态、错误 UUID、非法摘要字符均被 `parse` 拒绝；`RequireVerified` 对 P0 组合返回 unverified，对 sing-box `policy.round_robin` 返回 unsupported。原“顶层 verified”负例使用空组合列表，实际因组合数量不足而被拒绝；该覆盖缺口在下方评审修复中补齐。

`docker build -f deploy/Dockerfile.check -t proxyloom-check:m0-t023 .` 通过，含 `go test -mod=readonly -race ./...`、`verify-cores.mjs` 与边界检查。镜像清单 `sha256:e4aaf5cc70e9bb6d23d2cc6b2c71cb705eed7c9678764d131eec6d85b68710a0`。

未执行：三内核配置生成、Runner 执行框架、客户端导入、两架构原生烟测、G0。

## 2026-09-07 评审修复复核

基于同一起点提交 `93295278b3e2f66a13cd326d89071b2ddebbe7ec` 的本地未提交改动。修复不改变六套构建锁、任务状态或能力状态。

先在完整内嵌组合文件中仅将顶层 `state: unverified` 改为 `state: verified`，新增回归在修复前失败：`parse` 返回 nil 而非 `ErrInvalidLock`。随后补充顶层状态检查，回归通过。当前生成的构建能力仍强制为 `unverified`；这次修复补齐输入校验，不代表此前存在能力放行绕过。

本轮 Windows amd64 已执行并通过：`go test -mod=readonly -count=1 ./internal/isolation ./internal/capability`、两个包的 `-race -count=1` 检查、`go vet -mod=readonly ./...`、`node scripts/verify-cores.mjs` 及边界、锁文件、PLAN 检查。Go 使用工作区 `.cache/go-build`、`.cache/gomod` 和 `.cache/gopath`，避免默认缓存目录的访问限制。

本轮 Docker 引擎连接报 permission denied，未重跑 Linux 容器检查；上文 Docker 镜像与构建成功记录属于修复前的历史证据，不能证明本次修改已通过 Linux race。

合并修复后，`node scripts/check.mjs` 在 Windows amd64 完整通过：格式、模块校验、`go mod tidy -diff`、静态分析、边界/锁/PLAN 检查及 `go test -mod=readonly -race ./...`。前端与本次修改无关，未重跑前端检查。
