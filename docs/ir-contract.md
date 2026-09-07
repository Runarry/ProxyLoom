# T-003：IR v1 与共享接口

本契约对应 `docs/PLAN.md` T-003、设计 §3/§17.1 和需求 §3.2。实现位置为 `internal/ir/`、`internal/adapter/` 和 `schemas/ir-v1.schema.json`。它提供 M0 原型的数据结构、严格校验及接口，不包含数据库、配置编译、真实内核执行、发布或网络能力验证。结构有效不代表 `verified`，也不代表 G0 或完整 AC-03/AC-08 已通过。

## 解析与版本

Schema 使用 JSON Schema draft 2020-12，消费者必须启用 `format` 断言。公开定义在 `$defs`，例如 `#/$defs/node`、`chain`、`resource`、`target_ref`、`frozen_input`。Go 内嵌同一文件，所有引用在进程内解析，禁止外部 Schema 文件及网络加载。

`DecodeNode`、`DecodeChain`、`DecodeTargetRef`、`DecodeResource`、`DecodeFrozenInput` 与相应 `UnmarshalJSON` 都严格检查字段名、联合类型及版本。拒绝未知字段（包括大小写变体）、重复键、尾随文档、无效 UTF-8、未配对 UTF-16 转义和不允许的 `null`。单文档上限 10 MiB、嵌套深度上限 128。版本只能为整数 1；JSON 的 `1.0`、`1e0` 按整数语义处理，修订最大值为 `int64` 上限，不经 `float64` 转换。

所有公开 IR 结构提供 `Validate`，供直接 Go 构造检查。该入口在序列化前检查字符串 UTF-8，避免 `encoding/json` 用替代字符悄悄改变密码。带接口的 Node/Resource 还检查精确的受支持动态类型，拒绝借嵌入类型伪造新的认证或载荷。调用者必须先 Validate；普通 JSON 编码不是校验入口。

## Node

Node 将 endpoint、auth、transport、security 分开，不存在 `detour`、`dialerProxy`、`dialer-proxy` 或链依赖字段。地址是小写 ASCII 域名（IDNA 应由导入层预处理）或独立 IP，不能带 URI 协议、路径、端口或 IPv6 方括号。WebSocket Host、endpoint.host 与 TLS server_name 保持独立。

| protocol | auth 具体类型 / kind | v1 结构范围 |
| --- | --- | --- |
| shadowsocks | MethodPasswordAuth / method_password | aes-128-gcm、aes-256-gcm、chacha20-ietf-poly1305；native_tcp + none |
| vmess | VMessAuth / vmess_aead | UUID 和显式 cipher；无旧 alter_id；native_tcp/websocket + none/tls |
| vless | UUIDAuth / uuid | native_tcp/websocket；none/tls；REALITY 仅 native_tcp |
| trojan | PasswordAuth / password | native_tcp/websocket + tls |
| socks5 | NoAuth / none 或 UsernamePasswordAuth / username_password | native_tcp + none/tls |
| http | NoAuth / none 或 UsernamePasswordAuth / username_password | HTTP CONNECT；native_tcp + none/tls |

这些枚举是首版结构候选集。具体内核版本、组合、cipher、fingerprint、ALPN、UDP、复用与 Vision 是否可生成/执行仍由后续能力清单和真实测试决定，不能据本表开放未经验证的发布能力。

TLS 显式携带 `server_name` 与 `verify_certificate`；Go 使用 `*bool` 要求调用方明确选择证书校验。REALITY 携带 server_name、32 字节 base64url public_key、偶数长度十六进制 short_id 和 client_fingerprint。features 的 udp/multiplex 使用 `*bool`，缺省、显式 false、显式 true 不混淆，null 被拒绝。可选 protocol_variant 当前只有 VLESS 原生 TCP TLS/REALITY 的 `xtls-rprx-vision`。首版 `extensions` 只接受 `{}`，连空的内核命名空间也不接受。

IR 中的 auth 是完整、可用于编译的真实值，**不是管理 API 的秘密补丁 DTO**。管理 API 的缺省保留、null 清除、has_password、脱敏响应与重认证应另建类型，不能把掩码写回 IR。密码不接受空串或常见掩码。`Secret`、Node、认证对象、Resource、FrozenInputSpec、FrozenInput 和 Artifact 默认 fmt/slog 日志脱敏；显式 JSON 序列化仍包含编译秘密，不得写日志或普通存储。加密、密钥及内容 HMAC 实现留给 T-005。

## 资源与引用

资源形态为 `{ "metadata": {...}, "payload": {...} }`。metadata 固定包含 resource_id、scope_id、kind、revision、schema_version、name、tags、enabled、security_epoch。ID 使用规范小写 UUID，修订与 epoch 为正整数，tags 是无重复字符串集合。名称不是身份，也不要求唯一。metadata 枚举预留九种资源 kind；当前 Resource 只接受 Node/Chain，其他 kind 不能借任意 JSON payload 提前开放。

TargetRef 是严格判别联合：

```json
{"type":"resource_ref","kind":"node","resource_id":"11111111-1111-4111-8111-111111111111"}
{"type":"builtin","builtin":"reject"}
```

resource_ref 只含 type/kind/resource_id，kind 为 node/chain/policy_group；builtin 只含 type/builtin，值仅 direct/reject。混合分支被拒绝。`ValidateFor` 让使用字段进一步约束允许种类与 builtin，例如策略成员只允许 node/chain；预留 policy_group 引用不会开放其 payload。

Chain 使用 `hops: [{node_id}, {node_id}]` 和 `failure_policy: "fail_closed"`。P0 恰好两跳，ID 必须不同。数组顺序为客户端视角，首跳 A、末跳 B，B 是出口。编辑态跟随节点头，冻结后只通过快照中唯一的具体资源修订解析。链作为独立资源存在，不更改 Node。

## 冻结输入

`NewFrozenInput(FrozenInputSpec)` 是唯一创建可用 FrozenInput 的入口，JSON 解码同样调用它。FrozenInput 没有导出可变字段，零值校验失败；输入和 `Spec()` 返回值都深拷贝认证对象、布尔指针、transport 参数、ALPN、来源、tags、hops、资源、成员和目标。`Target(key)` 返回不含指针的目标值。句柄复制只共享不可变内部快照。

构造时要求固定 snapshot_id/scope_id、catalog_revision、scope security_epoch、每个资源的具体 revision/security_epoch，以及每个成员的匹配修订/epoch。校验所有资源启用、scope 一致、资源 ID 与目标 key 唯一、链跳存在且是具体 Node、成员种类/修订/epoch 匹配，以及无成员依赖闭包之外的额外资源。额外资源被拒绝以避免无意分发其凭证。Schema 只验证结构，闭包等跨对象语义由 Go 补充；夹具 manifest 明确区分两层期望。

仅集合做排序：resources 按 ID、members 按 ID、targets 按 key、tags 按字典序。hops、ALPN 等有序数组保持原序。该序列化稳定性测试不等于后续目标配置编译的确定性验收。

**M0 FrozenInput 仅包含 node/chain 闭包和目标身份，不是完整发布冻结模型。** Target 固定 core_family、core_build_id、core_build_sha256、adapter_version、client_preset_id/revision 与输出格式。这里的 ID/摘要只是冻结声明，尚不证明构建注册、预设内容或能力已验证。T-024 原型必须使用显式冻结、或与编译器版本绑定的受控预设，不能凭 preset ID 在 Compile 内访问数据库/网络；T-022/T-032 再扩展完整的预设及发布依赖载荷契约。

## 编译与执行接口

`adapter.Compiler.Compile(ctx, FrozenInput, Target)` 返回 Artifact、Diagnostic 列表与错误。实现必须 Validate 输入，并要求目标与 `input.Target(target.Key)` 完全一致，只依赖冻结内容及固定编译器/构建代码，不读取数据库、网络或当前时间。错误或 unverified 能力必须阻止成功产物，不得直接回退。

`RunnerAdapter`（别名 `CoreAdapter`）提供 Family、ValidateSpec、RunSpec、RedactLog。CommandSpec 只有 registry ExecutableID、参数数组与 Runner 分配的 WorkingDir，不提供 shell 字符串。`CommandSpec.Validate` 检查与 Runner 已知构建/目录一致及无 NUL 参数；它不等于执行器安全验收。实际参数白名单、路径权限/符号链接处理、环境、进程回收、隔离和预算由后续 Runner 框架承担。Artifact 的 Bytes/ContentHMAC 和 CommandSpec.Args 提供 Clone，避免调用方意外共享可变缓冲区。此任务没有内核命令实现或执行操作。

## 诊断与验证证据

诊断使用稳定 `IR_*` 码、severity、resource_id、field_path、target_key、固定说明。field_path 为 RFC 6901 JSON Pointer，根为 `""`。已知字段可精准定位，例如 `/auth/password`、`/members/0/security_epoch`。未知字段名可能含凭证，因此仅定位安全的父对象；重复键的路径只保留固定契约字段及数组序号。无效 resource_id 不回显。库的原生 Schema 错误可能携带输入，IR 层只返回经转换的无值诊断。

`fixtures/ir/manifest.json` 登记独立 Schema 和 Go 期望，包含六类节点、Trojan A/B/A→B、三目标冻结样例与结构/语义反例。所有 `.invalid` 域名、认证、UUID、版本和构建摘要均为离线合成内容，不能当真实构建锁或连接证据。

本轮验证命令：`go test ./internal/ir ./internal/adapter ./schemas`；静态检查：`go vet ./internal/ir ./internal/adapter ./schemas`。测试覆盖严格字段/联合、缺省与 false、整数精度、Unicode、默认日志脱敏、引用/修订/epoch/闭包、输入及读出别名 mutation、只排序集合和独立 Schema-vs-Go 夹具。真实三内核校验、链路方向/无绕行、数据库事务冻结与发布测试尚未执行，属于后续任务。
