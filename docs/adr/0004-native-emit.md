# ADR-0004：M0 三目标原生 Emit

日期：2026-09-07。关联 T-025、T-026、T-027。状态：采用本窗口实施选择，交付评审待定。补充设计 §6–7 与 ADR-0003，不替换历史决策编号。

本窗口把 `Compile` 从规范 Plan JSON 换成可交给锁定内核检查的完整原生配置。不宣称任何组合 `verified`，不实现完整 P0 协议／策略／DNS 映射，不跑 T-028 真实链路。

1. `Prepare` 仍负责冻结校验、构建认证、稳定标签与两跳展开。家族 Emit 只消费 `adapter.EmitInput`，不复制公共逻辑。
2. `adapter_version` 升为 `0.1.0-m0-native`。能力状态仍全部 `unverified`。
3. 本轮只映射 Trojan TCP/TLS。Xray 链用 `dialerProxy`，sing-box 用 `detour` 并拒绝与其它拨号字段并存，Mihomo 用 `dialer-proxy` 且禁用隐式资源下载与 `MATCH,DIRECT`。
4. 生成配置含回环 SOCKS／mixed 入口与指向业务出口的路由；独立节点与链实例标签隔离，不修改原 Node。
5. 真实内核检查继续走锁定 Debian、`--network none` 与 `internal/runner/exec` 的固定 argv。`scripts/verify-compile-exec.mjs` 先回归手写配置，再检查编译器生成字节。

代价：Plan 仍不是发布物；原生检查通过不能证明 A→B 连通或客户端可导入。Windows 宿主仍不能本地执行 linux 内核。T-029 再补协议与策略映射。
