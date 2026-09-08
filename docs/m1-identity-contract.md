# M1 身份接口与运行约定

范围：T-006，仅管理员初始化、认证、受控重置及未来敏感操作守卫。节点、发布、订阅和 Runner 接口未在本窗开放。

## API

| 方法和路径 | 输入／输出 | 保护 |
| --- | --- | --- |
| POST /api/v1/setup | setup_token、username、password → 用户投影、CSRF、Cookie | 一次性主机凭证、Origin、JSON、数据库初始化锁 |
| POST /api/v1/auth/login | username、password → 用户投影、CSRF、新 Cookie | Origin、JSON、账户和可信源 IP 限速 |
| GET /api/v1/auth/me | 当前 Cookie → 用户投影和 CSRF | 数据库实时会话校验 |
| POST /api/v1/auth/reauth | password → 最近认证截止时间 | Cookie、Origin、CSRF、JSON、限速 |
| POST /api/v1/auth/logout | 空请求体或 {} → acknowledged | Cookie、Origin、CSRF、JSON；数据库吊销成功后清 Cookie |

所有响应 no-store。认证错误使用既有固定错误码；请求 ID 来自服务器，日志不含请求正文、用户名原文、Cookie、CSRF 或密码。认证操作无资源 If-Match，不使用保存秘密的幂等响应。

管理 API 只接受管理 Cookie。Authorization 头或客户端证书不能代替管理会话；内部 Runner 与订阅路径继续保持独立边界。`fixtures/api/implemented-routes.json` 列出五个实际接口，测试从 Gin 实际路由表核对；OpenAPI 其余接口保持 contract-only。

会话绝对寿命 8 小时、空闲 30 分钟；显式重认证 5 分钟有效。认证成功、失败、退出、重认证、重置及受控敏感读取进入追加审计；需审计的状态变更在审计失败时回滚。重置与会话签发通过数据库事务串行化，不允许迟到登录恢复旧 auth_version。

## 配置与主机命令

- `PROXYLOOM_SETUP_TOKEN_FILE`：可选，文件为 32 随机字节的无填充 base64url（43 字符），允许一个末尾换行。缺省时禁用 setup；不影响已存在管理员登录。
- `PROXYLOOM_TRUSTED_PROXIES`：可选，以逗号分隔的明确 CIDR；默认不信任任何代理。只为可信直连对端解析转发 IP，Origin 永远以配置的 PUBLIC_URL 为准。
- `PROXYLOOM_TOKEN_PEPPER_FILE`：沿用独立 32 字节原始密钥文件，身份服务使用隔离用途；更换 pepper 使已有会话失效。

创建初始化凭证（输出路径必须绝对路径，文件已存在则拒绝）：

```text
proxyloom-server admin create-setup-token --output /secure/proxyloom/setup_token
```

先在受控主机建立仅授权操作者可访问的父目录，再生成及挂载文件。CLI 文件默认 0600；容器以 UID 10001 运行时须显式让该 UID 可读。Windows 应使用限制 ACL 的目录。命令不打印凭证，初始化完成后可移除挂载和配置。

重置管理员密码：

```text
proxyloom-server admin reset-password --username admin --password-file /secure/proxyloom/new_password
```

该命令只读取 runtime DSN 与 token pepper 文件；受控密码文件最多 1024 UTF-8 字节，可含一个末尾换行，不做普通空白裁剪。新密码须 12 字节起。重置使该管理员所有旧会话失效，不创建管理员、不复活 setup。保护好命令所用 DB 权限与主机访问权限。

开发 Compose 经 `scripts/dev-init.ps1` 初始化独立 setup_token 文件；旧完整开发秘密组保留，仅缺失的 setup_token 被创建。生产不使用这些开发凭证。Runner 不挂载任何新增秘密。

## 验证边界

必须同时具有真实 PostgreSQL 身份负例／并发证据和 HTTPS Cookie/CSRF 客户端验证。单测中的数据库 skip 不属于验收通过。T-006／T-008 自动验收完成后仅转 in_review；G1、节点 CRUD 和完整管理页面仍需后续任务。
