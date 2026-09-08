# ADR-0008：M1 管理员身份与质量门槛

日期：2026-09-08。状态：已实现，T-006／T-008 待评审。依据：用户批准的下一窗口方案、SRS FR-001／002、SDD §12.2。

## 决策

身份服务与 catalog 分离，PostgreSQL 持久化一次性初始化状态、管理员、会话、双层限速及追加审计。认证状态不改变 catalog_revision、节点 revision 或 security_epoch。P0 固定单工作空间，HTTP 不接受客户端指定 scope。

管理员密码使用 Argon2id（64 MiB、3 次、并行 1），独立随机盐及版本化哈希。新密码 12～1024 UTF-8 字节，登录保留原始密码，无大小写或空白归一化；用户名为 1～64 个小写 ASCII 字母、数字、点、下划线或连字符。

会话 Cookie 为 32 随机字节的 base64url，验证摘要及 CSRF 派生采用专用域与 token pepper，不复用 content HMAC 或节点加密密钥。会话数据库检查包含 auth_version、scope.auth_epoch、8 小时绝对到期及 30 分钟空闲到期。显式重认证建立 5 分钟标记；普通登录不建立该标记。

## 契约差异

T-007 OpenAPI 的 SameSite=Strict 与 SDD §12.2 的 Lax 不一致。本次按用户批准方案统一为 Lax，保留 HttpOnly、Secure、Path=/ 及严格 Origin + CSRF 防御。仅显式 development 且 PUBLIC_URL 为 HTTP 回环地址时省略 Secure；该例外不能用于生产。

CurrentUser 新增必需 csrf_token，由 setup/login/me/reauth 的脱敏用户投影显式返回；会话 ID 不进入响应 JSON。请求通过 X-CSRF-Token 提交，初始化和登录在尚未建立会话时豁免此头，但必须提交精确 Origin 和 application/json。logout 允许空请求体或空对象，拒绝未知字段。

初始化凭证文件可在初始化后撤下；缺省配置只禁用 setup。已配置但不可读或格式非法则启动失败。受控主机重置只更改管理员密码与会话，不重开 setup，不重置工作空间 auth_epoch，不改节点凭证。

## 质量与证据

既有工程检查继续复用；补充秘密扫描、精确契约差异声明、实际挂载路由清单、PR 模板及身份实库验收。契约变化必须带文档、夹具和生成物；声明是可评审的变更记录，不是自动允许后续任意变化。远端分支保护需实际查证，CI 失败不等同于已强制阻止合并。

实现和验收详情见 `docs/m1-identity-contract.md`、`docs/quality-gates.md`及 T-006／T-008 证据。本 ADR 不升级内核 verified 或宣称 G1 完成。
