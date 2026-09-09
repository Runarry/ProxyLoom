# M1 来源身份、SafeFetcher 与两跳链接入

本契约实现 ADR-0010。来源 HTTP、刷新调度、覆盖合并、编排 UI、URI 导出和依赖图不在本窗口。

## 身份匹配（T-012）

`internal/origin` 不访问网络或数据库。指纹由调用方用既有 `PurposeImportConnection` HMAC 提供，匹配器只比较不透明摘要。

1. 同一来源内非空 `external_key` 精确相等 → `stable_external_key`（身份）。
2. 候选项带 `source_item_id` 且已有人工绑定 → `manual_binding`（身份）。
3. 连接内容指纹完全一致 → `exact_fingerprint`（重复检测，不是身份确认）。
4. 协议+主机+端口相同，或同协议且名称相同，但指纹不同 → `suggestion`。禁止自动填入可提交的 `existing_resource_id`。

多条命中同一级时标为冲突，不挑选。改名不改变指纹，因此不破坏已有节点的重复提示。导入预览继续允许对指纹命中执行显式 update；建议态保持 `new`。

`external_key` 仅当元数据含 JSON 字符串且匹配 `^[A-Za-z0-9._:~-]{1,128}$` 时采用。忽略 `name`、`id`、UUID、密码及其他 URI 字段。

## SafeFetcher（T-013）

`internal/safefetch` 不持有数据库或主密钥，也不发给 Runner。

- 默认 HTTPS；HTTP 必须显式 `AllowHTTP`。
- 拒绝 userinfo、fragment、空主机和非法端口。
- 每次拨号：解析或解析字面 IP → Unmap → 拒绝阻塞地址 → 仅连接剩余地址。
- 阻塞：未指定、回环、私网、链路本地、多播、接口本地组播、CGNAT `100.64.0.0/10`、`0.0.0.0/8`、`192.0.0.0/24`、`240.0.0.0/4`、IPv6 `2001:db8::/32`、`fd00:ec2::254`。IPv4-mapped 先 Unmap。
- TLS ServerName 与 HTTP Host 使用 URL 主机，不改成拨号 IP。
- 每个重定向重复 URL 与地址检查；跨 scheme/host/port 删除 Authorization。
- 默认超时 15s、最多 3 跳（上限 5）、压缩后与解码后各 10 MiB。
- `Proxy` 为 nil，不调用 `ProxyFromEnvironment`。
- `AllowNets` 只给测试；空列表是生产语义。

错误码固定且不含 URL、IP、认证头或响应体。

## 两跳链（T-017）

`GET/POST /api/v1/chains` 与 `GET/PATCH/DELETE /api/v1/chains/{id}`。会话、CSRF、Origin、If-Match 与节点管理相同。

写入 hops 必须恰好两个不同 `node_id`，目标为当前 head、kind=node、未删除且 enabled。缺失、停用、软删或非 node 返回 422，details 指向 `/hops/{i}/node_id`。过期 If-Match 仍为 412。链软删除推进自身 security_epoch，不改 hop 节点修订。

编辑态引用跟随节点 head；`resource_refs` 由载荷提取。A→B 与 B→A 是不同资源。OpenAPI `uniqueItems` 拒绝重复 hop；策略组不能出现在 Chain hops 字段。

## 明确不做

来源资源与 `/api/v1/sources*`、`source_refresh` 任务类型领取、覆盖 typed merge、T-016 导出、T-018 依赖图、T-049 编排页、能力 verified。
