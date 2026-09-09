# M1 来源管理闭环与策略组契约

本窗口覆盖 T-014／T-015／T-016 补齐、T-019、T-048。既有来源、覆盖与节点接口保持；本文件记录新增行为。

## 来源批次与确认

`source.latest_preview_batch_id` 指向 `GET /api/v1/imports/{id}`。来源批次包含 `source_id`、`snapshot_id`、`source_revision`，候选使用现有每页最多 200 条的分页，保留期限沿用七天。成功刷新由 Worker 内部创建批次；浏览器不上传来源原文来绕过抓取层。

候选增加 `source_item_id`、`change_kind`（new／modified／missing／conflict／unchanged）、`auto_applied`、可选 `binding_revision`，并返回 `upstream_changes` 和 `effective_changes`。差异元素为 `field_path` 与非秘密 `before`／`after`；秘密只返回 `secret_changed=true`，绝无原值或新值。路径来自类型化节点与名称，不来自任意上游元数据。

`POST /api/v1/imports/{id}/commit` 使用批次 If-Match、Idempotency-Key、来源 `source_revision`，已绑定目标还须在对应 decision 提供 `expected_binding_revision`。update／bind 沿用 resource_id 与 expected_revision。旧占位的顶层 binding_revision 继续拒绝。

manual 的 create／update／bind／skip 在同一事务内应用；候选及目标不匹配、过期修订、其他来源绑定占用等导致整批失败。提交不访问网络。safe_updates 已自动应用及 unchanged 候选不允许重复写入（skip 可以）；missing 保留 stale 标记并服从现有消失策略。

新成功刷新或来源配置编辑将旧待确认批次置为 superseded。失败刷新保留最近成功快照和预览，不因仅更新运行错误状态就使可用预览失效。来源及目标条件在真正提交事务内再次检查，成功重放返回原回执。

来源条目分开存储最新上游和已确认基线；本地覆盖编辑基于已确认基线。绑定被明确更新或自动应用时同步推进，避免确认前意外修改有效节点。

分享链接查询字段及 VMess JSON 可携带显式 `external_key` 字符串（1～128 个既有身份白名单字符）。解析器保留其隔离元数据，来源匹配使用它维持连接参数变化后的身份；无效值拒绝。该字段不进入 Node 编译载荷，也不被当成远端协议参数。

## 页面与覆盖

来源页面提供管理、刷新、周期设置、最近成功时间、错误及预览入口。新建默认 manual、retain、关闭周期刷新。保存已有来源时 URL／认证未改则省略，显示值不能作为原始值回写。

绑定节点编辑只提交实际修改的字段并携带 binding_revision；409／412 保留草稿，加载最新修订后显式重试。恢复覆盖使用后端返回的 overridden_fields 路径。Endpoint 沿用现有契约作为完整地址与端口字段，按 `/endpoint` 整体覆盖／恢复；不伪装为可独立恢复的两个字段。

## 策略组

`GET/POST /api/v1/policy-groups` 与 `GET/PATCH/DELETE /api/v1/policy-groups/{id}` 落地现有契约。四种策略分别为 fixed、manual_select、latency_best、round_robin；1～200 个唯一 Node／Chain 成员，默认项必须属于成员，on_unavailable 固定 fail_closed。

写入检查成员、链两跳的存在性、启用状态、类型与工作空间。修订、ETag、审计、分页、反向引用和停用语义复用现有资源目录。策略引用链时依赖展开包含其两个节点。

目标覆盖只允许 strategy／default_member／health_check，经纯合并后再次验证。目标持久化和真实内核策略映射仍属后续任务；健康检查配置不执行平台网络请求，编译遇到尚未实现策略明确拒绝。

## 导出

`POST /api/v1/exports` 的资源格式增加 base64_uri_list，内容为同一 URI 列表 UTF-8 字节的标准 Base64。uri_list／proxyloom_json 保留。最多 200 个指定修订的 Node，include_secrets=true，近期重认证及 private.export 审计继续适用。

URI 无法表达的字段返回 422，details 定位 `/resources/{index}/node/...` 与 resource_id；错误不携带节点内容。链／策略组不允许节点格式导出。前端下载只在内存中处理秘密并释放 Blob URL。

## 验证边界

专项验证覆盖 manual／safe_updates、差异脱敏、覆盖与恢复、陈旧批次、绑定及节点竞态、原子性与幂等、策略引用和四策略持久化、Base64 导出与重认证。真实浏览器使用测试专用本地来源夹具、真实 API 和临时 PostgreSQL；不放宽生产抓取规则。

任务证据保存于对应 docs/evidence/T-xxx；本窗口不批准 G1，不将管理功能成功外推为内核策略已 verified。
