# T-014 远程来源与周期刷新

挂载 `GET/POST /api/v1/sources`、`GET/PATCH/DELETE /api/v1/sources/{id}` 与 `POST /api/v1/sources/{id}/refresh`。来源是加密 `kind=source` 修订；普通 GET 只返回 `url_display` 与认证存在性。刷新入队 `source_refresh`，`batch_id` 为 source id，冲突 409。API Worker 使用 SafeFetcher 抓取；非 2xx、空体、解析失败写入 `last_error` 与失败快照，不替换最近成功快照与条目。`source_schedules.next_run_at` 可在重启后恢复。`safe_updates` 对身份命中更新连接并保留名称；全新条目创建节点。追加迁移 `000009_sources.up.sql` 已在本窗口基线上，不改写 000001–000008。

验证：`internal/source` 单测覆盖 URL 展示与 redaction；真实 PostgreSQL `TestPostgresSourceCRUDRefreshFailClosedAndUniqueJob` 覆盖 CRUD、redacted GET、可恢复调度行、并发刷新 409、成功导入节点、失败不空覆盖。foundation 运行 `638c3547-f8af-4f57-ab6f-2b58ab4d7fb8`。OpenAPI 对应操作标为 implemented；契约声明 `compat/contract-changes/T-014.json`。未实现 T-015 覆盖表、T-016 导出或来源管理 UI。
