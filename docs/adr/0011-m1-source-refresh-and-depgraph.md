# ADR-0011：M1 远程来源刷新与依赖展开

日期：2026-09-09。状态：按已批准的 T-014／T-018 窗口实施。验收以逐任务证据为准。

## 决策

本窗口实施 T-014 与 T-018。不实施 T-015 覆盖合并、T-016 URI 导出、策略组／路由／DNS HTTP、G1 关闭或能力 verified。

**T-014** 将来源存为 `resources.kind=source` 的加密修订，不是编译 IR。管理 GET 只返回 `url_display` 与认证存在性。刷新由 API Worker 执行 `source_refresh`，`batch_id` 等于 source id，同一来源最多一个非终态任务。Runner 不能领取。抓取使用 `internal/safefetch`；管理员保存的 `http://` URL 视为显式允许 HTTP；生产 `AllowNets` 为空。失败只记录 `last_error` 并追加失败快照，不替换最近成功快照与条目。调度把 `next_run_at` 存在 `source_schedules`，进程重启后按数据库时间恢复。`commit_mode=manual` 只更新快照与条目；`safe_updates` 对身份命中更新连接并保留名称，全新条目创建节点与 binding，建议与指纹重复不自动合并。

**T-018** 提供 `internal/depgraph.Expand`。缺失、错 kind、循环、超限与排除必要依赖定位到字段路径。链写入在 hop 校验之后展开草稿闭包。本窗口不挂载独立 expand HTTP。

## 验证

来源 CRUD、唯一刷新、失败不空覆盖以真实 PostgreSQL 与回环 httptest 验收。依赖展开以单测与链 hop 校验覆盖。OpenAPI 将 `/api/v1/sources*` 标为 implemented，并声明契约差异。
