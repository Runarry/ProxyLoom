# ADR-0012：M1 人工覆盖、冲突合并与节点导出

日期：2026-09-09。状态：按已批准的 T-015／T-016 窗口实施。验收以逐任务证据为准。

## 决策

本窗口实施 T-015 与 T-016。不实施策略组／路由／DNS HTTP、T-048 来源 UI、G1 关闭或能力 verified。T-012／T-013／T-014／T-017／T-018 保持 `in_review`。

**T-015** 在 `node_bindings` 上追加加密 `override_envelope` 与 `origin_state`（`active`／`stale`／`conflict`）。来源基线仍在 `source_items`；有效节点由类型化 merge 计算，不深合并任意 JSON。刷新在身份命中时更新基线并保留覆盖字段；建议或歧义标 `conflict` 且不自动绑定；条目消失标 `stale` 且不删除节点。绑定修订 CAS 失败不覆盖补丁。`protocol` 不可覆盖。名称覆盖独立于 Node IR。

**T-016** 挂载 `POST /api/v1/exports` 的资源导出。`uri_list` 仅接受 `kind=node` 的指定修订；链／策略／路由／订阅被拒绝。含秘密导出要求近期重认证并审计 `private.export`。`include_secrets=false` 失败关闭。发布物导出仍为 contract-only。

## 验证

覆盖 merge、stale／conflict、CAS 与恢复以来源刷新和节点 PATCH 的真实 PostgreSQL 验收。URI 往返以解析夹具与六类协议单测覆盖。OpenAPI 将节点 binding 字段、来源 items 与 `/api/v1/exports` 标为 implemented，并声明契约差异。
