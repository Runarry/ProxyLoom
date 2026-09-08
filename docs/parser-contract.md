# T-009：分享链接解析与批量标准化契约

实现位于 `internal/importparse/`，离线夹具清单位于 `fixtures/imports/manifest.json`。对应需求 FR-003／FR-004、AC-02／AC-03 与设计 §5.1。这里只生成经过 IR 校验的候选项，不访问网络、不安装或执行插件、不写正式节点库，也不宣称任何内核组合已经验证。候选的加密、去重提示和选中提交由 T-010 导入服务负责。

## Go 接口与秘密边界

`Parse(ctx context.Context, format string, input []byte) (Result, error)` 接受 `auto`、`text`、`base64`；空格式按 `auto`。`Result` 包含实际 `Format`、列表包装的 `Base64Layers` 以及按来源顺序排列的 `Candidates`。取消请求返回 `context.Canceled`／`context.DeadlineExceeded`；批次错误返回静态 `ir.Diagnostics`，结果中不带部分候选。

`ParseURI(raw string) Candidate` 用于单条链接。`Candidate.Index` 是从零开始的非空条目序号，`Line` 是解码后从一开始的物理行号。单条入口固定为 0／1。空行不产生条目；保留混合列表中有效、无效和不支持的项。`Status` 为 `valid`、`invalid` 或 `unsupported`；只有 `Valid()` 为真时才含 `Node *ir.Node`。缺少标签时 `Name` 为空，显示名和资源 ID 由后续服务选择，不以名称进行身份合并。

`Dialect` 是固定版本标识：`ss-sip002`、`vmess-json-v2`、`vless-uri-v1`、`trojan-uri-v1`、`socks5-uri-v1`、`http-uri-v1`、`https-uri-v1`。它描述已经实现的解析规则；未知 scheme 不被猜测成其他协议。

`Metadata` 是 `map[string]json.RawMessage` 的命名类型。query 未知字段保存成 JSON 字符串，VMess 未知字段保留原始 JSON 值，始终隔离在 `Node` 之外。仅 `x-*`、`remark`、`remarks`、`group`、`tag`、`name` 属于此方言明确允许的描述性元数据，产生 `IMPORT_UNKNOWN_METADATA` 警告。其他未知字段也保留，但候选为 `unsupported`，避免把未知连接参数作为无关信息丢弃；`plugin` 同样处理。未知字段名和字段值均可能包含秘密，不出现在诊断文本或路径中。

所有认证值、名称、元数据与候选 JSON 都按敏感输入处理。Candidate、Result、Metadata 支持默认 fmt／slog 脱敏；显式 JSON 序列化保留真实值，必须交给加密存储，不能直接作为管理 API 响应或日志。解析器不保存原始 URI；原始输入的加密和保留期由导入服务管理。

`CanonicalConnection(ir.Node) ([]byte, error)` 在校验后序列化节点副本并移除 `Origin`。名称、资源 ID 和修订不在 Node 内，自然不参与连接内容比较。输出包含真实认证值，仅可交给带业务域隔离的 HMAC；它不是公开摘要，不用于推断来源稳定身份。ALPN 顺序、显式 features 与认证差异不会被合并。

## 输入与资源限制

| 边界 | 规则 |
| --- | --- |
| 解码后内容 | 最多 10 MiB，包含 UTF-8 BOM；只接收有效 UTF-8 |
| 原始输入 | 最多 18,641,356 字节，即最大正文的两层带填充 Base64 长度；原始包装空白也计入 |
| 非空条目 | 最多 5,000；先计数，超限返回整个批次错误 |
| 单 URI | 最多 16 KiB；超限项标为无效，其他条目仍可预览 |
| 列表包装 | 最多两层；SS userinfo／VMess JSON 必需的协议内部编码不属于列表包装 |
| VMess JSON | 受单 URI 限额约束；嵌套最大 32 层；所有层级拒绝重复键 |

UTF-8 BOM、LF、CRLF 和 CR 行分隔受支持，条目首尾空白移除，内部字段不会整体 URLDecode。没有注释语法：非空注释行也是待诊断条目。JSON／YAML 原生配置及 gzip／ZIP／XZ 签名直接拒绝；不解压内容，因此不发生压缩膨胀。

`auto` 首先识别原生配置并拒绝，再识别显式 URI 列表，否则尝试严格 Base64。混合列表中的任意显式 URI 可以确立文本列表类型，其他错误行不会阻断有效项。`text` 完全禁用 Base64 检测；`base64` 要求至少一层有效列表包装。支持标准／URL-safe 字母表及有／无填充，拒绝混合字母表、无效 padding 和非零尾部填充位。列表包装仅可移除 ASCII 空格、制表符、CR、LF；协议内部编码不接受空白。

## 已冻结的 URI 方言

下表中的默认值只属于这里定义的方言。没有认证默认值；用户名、密码或 UUID 缺失时绝不补造。endpoint、WebSocket Host 与 TLS server_name 分别解析和规范化：域名通过现有 IDNA Lookup 转为小写 ASCII，去除单个终止点；IP 使用规范文本，IPv6 URI 必须有方括号，带 zone 的 IPv6 被拒绝。URI userinfo、query、fragment 分别解码一次；fragment 的 `+` 保留，query 的 `+` 按表单编码表示空格，VMess JSON 内字段不再 URL 解码。

| 协议 | 认证与地址 | 传输／安全规则 |
| --- | --- | --- |
| SS SIP002 | 明文百分号转义 `method:password` 或 Base64 userinfo；显式端口。方法仅 `aes-128-gcm`、`aes-256-gcm`、`chacha20-ietf-poly1305` | 原生 TCP、协议内置加密；仅允许空路径或 `/`；插件被隔离并标为不支持，绝不忽略后导入可执行节点 |
| VMess JSON v2 | 链接主体必须为一层 Base64 JSON；必需 `v=2`、`add`、`port`、`id`、`aid=0`、`scy`、`net`、`tls`。`v`／`port`／`aid` 可为十进制整数字符串或整数 JSON；`scy` 必须为现有 IR cipher | `net` 为 `tcp`／`ws`；`tls` 为 `""`／`none`／`tls`；`type` 只允许缺省、空串、`none`。旧 alter ID、TCP HTTP 伪装或其他网络类型不支持 |
| VLESS | userinfo 仅为 UUID；显式端口；`encryption` 缺省或 `none` | `type` 缺省 `tcp`，支持 `tcp`／`ws`；`security` 缺省 `none`，支持 `none`／`tls`／`reality`；`flow` 仅 `xtls-rprx-vision`，且仅原生 TCP 的 TLS／REALITY |
| Trojan | userinfo 仅为完整密码；显式端口；密码中的冒号等保留字符必须百分号转义 | `type` 缺省 `tcp`，支持 `tcp`／`ws`；`security` 缺省且必须为 `tls` |
| SOCKS5 | 无 userinfo 明确表示 no-auth；存在 userinfo 时用户名和密码必须同时非空；显式端口 | 仅原生 TCP；`security` 缺省 `none`，可显式 `tls` |
| HTTP CONNECT | 同 SOCKS5 的认证规则；`http` 缺省端口 80，`https` 缺省端口 443 | 仅原生 TCP；`http` 固定 `none`，`https` 固定 `tls`；query 不可与 scheme 冲突；普通 URL 路径仅允许空或 `/` |

通用 query `type`、`security`、`path`、`host`、`sni`、`alpn`、`fp` 按协议范围处理；任何不适用的非空设置不能静默丢弃。WebSocket path 缺省 `/`，Host 可省略；URI 显式空 path／Host 被拒绝。VMess JSON v2 的 `host`、`path`、`sni`、`alpn`、`fp` 常规空占位符按省略处理，非空的 TCP host／path 或无 TLS 的安全字段被拒绝。

TLS 始终显式输出 `verify_certificate=true`；SNI 缺省为 endpoint.host，不从 WebSocket Host 推断。`allowInsecure`、`insecure`、`skip-cert-verify` 只允许其中一个且值为 `0`／`false`；开启、无效值、重复别名或用于无 TLS 连接均失败。VMess 的 `allowInsecure` 也支持 JSON 布尔值。ALPN 为按序逗号分隔的非空、不重复、至多 255 字节协议标识；TLS fingerprint 按 IR 约束检查，不借此宣称内核兼容。

REALITY 仅支持 VLESS 原生 TCP，必须显式提供 `sni`、`pbk`、`sid`、`fp`。`pbk` 为 32 字节无填充 Base64url 公钥，`sid` 为 0～8 字节偶数长度十六进制，可显式为空；十六进制规范化为小写。不补造公钥、short ID、fingerprint 或 server name；不得携带证书校验关闭参数。

所有重复 query key 在百分号解码后比较，包括相同值和未知键；大小写变体不作为已知字段别名。VMess 在所有层级拒绝重复 JSON 字段、转义后同名字段、无效 UTF-8、未配对 UTF-16 代理转义、尾随文档和超深嵌套。诊断采用固定 `IMPORT_*` 或已有 `IR_*` 代码和静态安全路径，不包装原生 URL／JSON 错误。

## 验证

`fixtures/imports/manifest.json` 的模板只包含 `.invalid` 域名、文档 IPv6 地址和占位符，测试运行时以 `crypto/rand` 构造认证、UUID、公钥与 Base64 载荷。单元测试覆盖六协议、Unicode／IPv6／百分号边界、缺认证、重复键、未知参数隔离、默认日志脱敏、原生配置拒绝、精确体积／数量／层数限制、来源位置及 fingerprint 输入。`FuzzParseURI` 与 `FuzzParseBatch` 使用无凭证种子，验证任意输入不能产生无诊断失败、超预算结果、不稳定序列化或无效 IR。

本契约不把 URI 解析、IR 校验或模糊测试等同于真实内核校验。真实内核、网络拨号、客户端导入和全部 P0 映射的证据仍属于对应适配与验收任务。
