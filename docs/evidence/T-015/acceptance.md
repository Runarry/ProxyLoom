# T-015 人工覆盖与冲突合并

2026-09-10 补齐：最新上游与已确认基线分开保存，本地覆盖不带入尚未确认的 manual 数据；来源预览提交与显式绑定同步检查节点及绑定修订。来源和覆盖管理页面接入逐字段恢复及冲突保稿。联合证据见 `docs/evidence/T-019/acceptance.md` 与 `docs/evidence/T-048/source-window/acceptance.md`，下方保留前窗口记录。

来源基线仍在 `source_items`。`node_bindings` 增加加密 `override_envelope` 与 `origin_state`（`active`／`stale`／`conflict`）。有效节点由 `internal/override` typed merge 计算。绑定节点 PATCH 写覆盖；`binding_revision` CAS 失败返回 409。`restore_fields` 删除补丁路径。建议／歧义匹配标 `conflict` 且不自动绑定；条目消失标 `stale` 且不删除节点。`safe_updates` 身份命中保留覆盖名称。追加迁移 `000010_overrides.up.sql`，不改写 000001–000009。

验证：`internal/override` 单测覆盖名称／端点 merge、协议拒绝与逐字段恢复。真实 PostgreSQL `TestPostgresOverrideStaleConflictRestoreAndExport` 覆盖名称覆盖在刷新后保留、模糊匹配可见 conflict、以及含秘密导出的重认证。foundation 运行 `87ee6a2a-6dcc-4517-84e1-39ec98214cbe`，`postgres_skipped_tests` 为空。来源 GET 返回 `items`。契约声明 `compat/contract-changes/T-015-override-and-export.json`。未实现来源管理 UI 或 G1 关闭。
