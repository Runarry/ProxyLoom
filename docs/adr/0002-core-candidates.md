# ADR-0002：M0 内核候选构建锁定

日期：2026-09-07。关联 T-023、T-053。状态：采用本窗口实施选择，交付评审待定。补充设计 ADR-05／§13.2／§17.5，不替换历史决策编号。

本窗口只锁定官方非预发布 linux 候选、能力状态模型和隔离夹具。不宣称任何组合 `verified`，不把内核装进 Runner 镜像，不实现编译器或执行框架。

1. 候选只来自 GitHub Latest **非 pre-release** 资产：Xray-core `v26.3.27`（`v26.7.x` 为预发布，不用）、sing-box `v1.14.0`、mihomo `v1.19.30`。URL 含具体 tag，禁止 `latest`。
2. 每个家族锁定 linux/amd64 与 linux/arm64。构建 ID 为命名空间 `8c3f0e2a-4b91-41d6-a2c1-9f0e6b7d4a10` 上 `family|git_tag|os|arch|binary_sha256` 的 UUID v5；同一摘要永不改 ID，不同二进制必须新 ID。
3. 归档 SHA-256 与解出二进制 SHA-256 均来自本机实际下载。amd64 在锁定 Debian 镜像中执行了 version 命令；arm64 只哈希、不执行。
4. 二进制不入库。`scripts/pin-cores.mjs` 写入 gitignored `.cache/cores/`。`adapter_version` 固定 `0.0.0-unverified` 直至 T-024。
5. P0 组合与 `chain.two_hop.tcp` 保持 `unverified`。sing-box `policy.round_robin` 按设计基线标 `unsupported`，这是适配意图，不是内核能力证明。
6. 隔离夹具（T-053）是独立 Trojan TLS 服务端和 HTTP 目标，供后续 G0 使用；test-only 网络策略不进入 `compose.dev.yaml`。

代价：预发布 Xray 功能不会进入本锁；官方构建特性以 version 输出和未验证清单为准，不能当作已支持矩阵。升级必须新增 CoreBuild，而不是改写现有 ID。
