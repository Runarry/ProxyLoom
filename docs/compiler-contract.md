# T-024：公共编译骨架

本契约对应 `docs/PLAN.md` T-024、设计 §6 与 NFR-04 原型。实现位于 `internal/compiler/`。它把已冻结的 Node/Chain 输入变成确定性的规范编译 Plan，不生成 Xray、sing-box 或 Mihomo 原生配置，也不把任何能力标为 `verified`。

## 入口

`compiler.Load()` 读取 T-023 锁定目录。`Compile(ctx, FrozenInput, Target)` 实现 `adapter.Compiler`。

1. 校验 FrozenInput。
2. `target` 必须与 `input.Target(target.Key)` 逐字段相等。
3. 用目录认证 `core_build_id` 与 `core_build_sha256`，家族与输出格式必须匹配（`xray_json` / `singbox_json` / `mihomo_yaml`）。
4. `adapter_version` 必须等于锁值 `0.1.0-m0-skeleton`。
5. 展开成员：独立 Node 得到 `n_<hex>`；两跳 Chain 得到 `c_<hex>_h1` 与 `c_<hex>_h2`，末跳 `dialer_tag` 指向 h1。不修改原 Node。
6. 序列化 Plan，`ContentType` 为 `application/vnd.proxyloom.compile-plan+json`。`ContentHMAC` 留空（T-005）。

`Prepare` 返回同一套图，供 T-025～027 替换 Emit。Compile 不读数据库、网络或当前时间；`ctx` 只用于取消。`client_preset_id` 只抄入 Plan，不按 ID 加载预设。

## 标签

哈希材料为 `node|<resource_id>|<revision>` 或 `chain|<resource_id>|<revision>` 的 SHA-256 小写 hex，起始 8 位。短哈希碰撞时只延长碰撞项（每次 +2），禁止随机盐。显示名不进入标签；改名不改 Plan 身份字节。

## 能力

未知构建、摘要不符、格式/版本不符、P0 列表外的组合、`unsupported`（如 sing-box `policy.round_robin`）均失败且无 Artifact。锁定但 `unverified` 的组合允许骨架产物，Plan 记录 `capability_state: unverified` 与 `CAPABILITY_UNVERIFIED` 信息诊断。`adapter_version` 为 `0.1.0-m0-skeleton` 时 Plan **不得**写出 `verified`，即使某条 catalog 记录已是 verified。这不是发布放行。

## Golden

`fixtures/compiler/frozen-chain-a-b.json` 钉真实 linux/amd64 构建。`fixtures/compiler/golden/xray-chain-a-b.json` 不含节点秘密。同一输入连续 Compile 100 次字节一致。

验证：`go test ./internal/compiler`。三内核原生配置、真实链路与 G0 属于后续任务。
