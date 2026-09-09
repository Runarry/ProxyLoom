# T-012 来源身份与去重

实现 `internal/origin` 纯函数匹配：`external_key` → 人工绑定 → 连接指纹（重复检测）→ 建议。指纹使用既有 `PurposeImportConnection` HMAC，不作为稳定身份。名称、UUID 和密码不能成为 `external_key`。本窗口未建 `source_items`／`node_bindings` 表，未实现来源 CRUD。

导入预览接入该匹配器。改名仍按指纹提示重复；同名且认证不同只产生 `IMPORT_IDENTITY_SUGGESTION`，状态保持 `new`，不填 `existing_resource_id`。

验证：

- `go test ./internal/origin` 正负例通过。
- 真实 PostgreSQL：`TestPostgresImportSuggestionDoesNotAutoMerge` 与既有指纹重复用例通过，见 foundation 运行 `b2068e8e-45e0-4d18-bb10-f44e23160072`。
