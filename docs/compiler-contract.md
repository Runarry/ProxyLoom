# T-024／T-025／T-026／T-027：编译入口与三目标原生 Emit

本契约对应 `docs/PLAN.md` T-024～027、设计 §6–7 与 NFR-04 原型。实现位于 `internal/compiler/` 与 `internal/adapter/{xray,singbox,mihomo}`。`Prepare` 把已冻结的 Node/Chain 输入展开成确定性图；`Compile` 对该图做家族原生 Emit，输出可直接交给锁定内核检查的完整配置。不把任何能力标为 `verified`。

## 入口

`compiler.Load()` 读取 T-023 锁定目录。`Compile(ctx, FrozenInput, Target)` 实现 `adapter.Compiler`。

1. 校验 FrozenInput。
2. `target` 必须与 `input.Target(target.Key)` 逐字段相等。
3. 用目录认证 `core_build_id` 与 `core_build_sha256`，家族与输出格式必须匹配（`xray_json` / `singbox_json` / `mihomo_yaml`）。
4. `adapter_version` 必须等于锁值 `0.1.0-m0-native`。
5. 展开成员：独立 Node 得到 `n_<hex>`；两跳 Chain 得到 `c_<hex>_h1` 与 `c_<hex>_h2`，末跳拨号指向 h1。不修改原 Node。
6. 按 `target.core_family` 调用独立 Emit，生成完整原生配置（回环入口、出站、指向业务出口的路由）。`ContentHMAC` 留空（T-005）。

`Prepare` 返回同一套图，供 Emit 复用，避免复制冻结校验、标签与链展开。`BuildPlan` 仍可从该图得到规范 Plan JSON（`application/vnd.proxyloom.compile-plan+json`），供调试与 T-024 Golden；`Compile` 不再把 Plan 当作内核检查输入。Compile 不读数据库、网络或当前时间；`ctx` 只用于取消。`client_preset_id` 只抄入 Plan，不按 ID 加载预设。

## 标签

哈希材料为 `node|<resource_id>|<revision>` 或 `chain|<resource_id>|<revision>` 的 SHA-256 小写 hex，起始 8 位。短哈希碰撞时只延长碰撞项（每次 +2），禁止随机盐。显示名不进入标签；改名不改编译字节。独立使用与链实例即使源 Node 相同也使用不同标签。

## 本轮原生范围

仅映射 Trojan／native_tcp／TLS。独立 A、独立 B 与 A→B 三类成员均生成完整配置。完整 P0 协议、策略、路由和 DNS 映射归 T-029。

链方向：业务路由指向末跳（h2）；末跳经第一跳拨号。Xray 使用 `streamSettings.sockopt.dialerProxy`，sing-box 使用 `detour`，Mihomo 使用 `dialer-proxy`。不写 Xray `proxySettings`，不把 Mihomo 旧 relay 或隐含 `DIRECT` 当作失败回退。Mihomo 关闭 `geodata-mode`／`geo-auto-update`，不启用外部控制器。

无法表达的字段（本轮范围外的协议／传输／`features.udp=true`／`multiplex=true` 等）返回 `COMPILE_UNMAPPED_FIELD`。sing-box 在 `detour` 与其它拨号字段并存时返回 `COMPILE_DIAL_CONFLICT`。失败时无 Artifact，不静默丢字段、改协议、降级为单跳或直连。

## 能力

能力目录的 `Load`、`Builds`、`Build`、`Lookup`、`Capability` 返回独立副本，含能力记录和嵌套证据列表。调用方修改返回对象不改变锁定目录或后续加载结果；该内存隔离修复不改变编译字节或能力状态。

未知构建、摘要不符、格式/版本不符、P0 列表外的组合、`unsupported`（如 sing-box `policy.round_robin`）均失败且无 Artifact。锁定但 `unverified` 的组合允许原生产物，Plan 记录 `capability_state: unverified` 与 `CAPABILITY_UNVERIFIED` 信息诊断。`adapter_version` 为 `0.1.0-m0-native` 时 **不得**写出 `verified`。原生配置检查通过不是发布放行，也不等于链路或客户端验证。

## Golden 与内核检查

`fixtures/compiler/frozen-chain-a-b.json` 钉真实 linux/amd64 构建。Plan Golden：`fixtures/compiler/golden/xray-chain-a-b.json`（无节点秘密）。原生 Golden：`xray-native-chain-a-b.json`、`singbox-native-chain-a-b.json`、`mihomo-native-chain-a-b.yaml`（含合成夹具密码 `EXAMPLE_ONLY_*`）。同一输入连续 Compile 100 次字节一致。

验证：`go test ./internal/compiler ./internal/adapter/...`。锁定真实内核正负检查与最小启动回收：`node scripts/verify-compile-exec.mjs`（先回归 `verify-core-exec.mjs` 手写配置，再检查本轮生成字节）。真实链路与绕行反例：`node scripts/verify-live-chain.mjs`（T-028）；G0 仍需人工评审。
