# T-016 URI 与 Base64 节点导出

2026-09-10 补齐：增加 base64_uri_list 格式及前端批量导出，保留 200 个指定修订节点上限、近期重认证和审计。URI 往返检查拒绝丢失字段，422 返回资源与字段路径。`TestPostgresBase64ExportRevisionReauthAndDiagnostics` 已在临时 PostgreSQL 通过；统一验证见 `docs/evidence/T-019/acceptance.md`，下方保留前窗口记录。

挂载 `POST /api/v1/exports`。本窗口实现 `type=resources` 的 `uri_list` 与 `proxyloom_json`。只接受 `kind=node` 的指定修订。`include_secrets` 必须为 true，并要求近期重认证与 `private.export` 审计。链、策略组及其他 kind 返回 422。`type=publication` 仍拒绝。导出不写修订。URI 由 `internal/importparse.EncodeURI` 生成，往返使用既有解析器。

验证：六类协议 encode/parse 单测；不安全 TLS 拒绝导出。含秘密导出无重认证为 403，重认证后 URI 可被现有解析器往返。与 T-015 同一次 foundation 运行 `87ee6a2a-6dcc-4517-84e1-39ec98214cbe`。契约将 `/api/v1/exports` 标为 implemented。
