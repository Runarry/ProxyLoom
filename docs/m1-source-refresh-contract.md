# M1 远程来源刷新与依赖展开

本契约覆盖 T-014 与 T-018。人工字段覆盖、stale 合并 UI、策略组／路由／DNS HTTP 仍不在本窗口。

## 来源（T-014）

来源是 `resources.kind=source` 的加密修订，不是编译 IR。普通 GET 只返回 `url_display`（scheme+authority）和认证存在性。URL 与请求头只在刷新执行路径解密。

管理 HTTP 挂载 `GET/POST /api/v1/sources`、`GET/PATCH/DELETE /api/v1/sources/{id}` 与 `POST /api/v1/sources/{id}/refresh`。刷新由 API Worker 执行 `source_refresh`，`jobs.batch_id` 等于 source id。同一来源最多一个 queued／leased／running 任务；冲突返回 409。Runner 不能领取该类型。

抓取使用 `internal/safefetch`。管理员保存的 `http://` URL 视为显式允许 HTTP；默认生产拨号不允许私网。超时、空响应、非 2xx、解析失败只记录 `last_error`，不删除或替换最近成功快照与条目。

周期调度把 `next_run_at` 存在 `source_schedules`。成功后按 `interval_seconds` 安排下一次；失败指数退避（60s 起，上限为刷新间隔）。进程重启后由数据库时间恢复，不依赖内存队列。

`commit_mode=manual` 只更新快照与 `source_items`。`safe_updates` 对身份命中更新连接字段但保留节点名称；全新条目创建节点并写入 origin／binding；建议态不自动合并。覆盖补丁表留给 T-015。

## 依赖图（T-018）

`internal/depgraph` 从根资源沿引用闭包展开。缺失、错 kind、循环、超限（默认 2000 个资源）定位到字段路径。`exclude` 命中闭包内必要依赖时返回错误而不是漏项。当前载荷只展开 Chain hops；Walker 可注入以便策略／路由／DNS 接入。
