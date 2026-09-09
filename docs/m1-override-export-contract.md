# M1 人工覆盖与节点导出

本契约覆盖 T-015 与 T-016。策略组、编排 UI 和发布物导出仍不在本窗口。

## 覆盖（T-015）

`source_items` 保存上游基线。`node_bindings.override_envelope` 保存类型化补丁（名称与 NodePatch 白名单）。有效节点 = merge(基线, 补丁)。数组整体替换；秘密 omit／null／value／遮罩语义与节点编辑 API 相同。`protocol` 出现在补丁中即 422。

`origin_state`：

- `active`：身份绑定有效
- `stale`：来源条目 `missing`，节点保留
- `conflict`：建议或歧义匹配，须 `origin_action=bind` 或 `skip`

节点 GET 在绑定存在时返回 `binding`（`binding_revision`、`origin_state`、`overridden_fields`、`source_item_id`、`source_resource_id`、`match_method`）。来源 GET 返回 `items`（id、state、name、external_key、node_id、suggested_node_id）。列表接口不带 items。

绑定节点的 PATCH 写覆盖而不是基线。`If-Match` 仍比较节点 head。覆盖或 `origin_action`／`restore_fields` 还比较 `binding_revision`；不匹配返回 409。`restore_fields` 按 JSON Pointer 删除补丁路径，有效值回到当前基线。`origin_action=bind` 仅在 `conflict` 时把匹配提升为 `manual_binding`。`skip` 不改节点载荷。

`safe_updates` 身份命中：更新基线，merge 后写节点修订；认证变化递增 `security_epoch`。`commit_mode=manual` 仍只更新快照与条目。

## 导出（T-016）

`POST /api/v1/exports` 实现 `type=resources`。`uri_list` 只导出 node。`proxyloom_json` 导出同一批节点的类型化 JSON。`include_secrets` 必须为 true，且需要近期重认证；false 返回 422。链或非 node kind 返回 422。不写修订。`type=publication` 仍拒绝。
