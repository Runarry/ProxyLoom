# T-020 路由与规则集验收

2026-09-10，基点 `80ad286a44c77a12c80384e70350951370be788e` 的 M1 未提交集成工作树。

已实现追加迁移 000013、类型化 RoutingProfile／RuleSet、五项 CRUD、分页、ETag／If-Match、不可变历史修订、引用、审计和软删除。路由规则有序首匹配，不同字段 AND、同字段 OR，显式 final；动作包含 direct／reject／Node／Chain／PolicyGroup。规则文本只在浏览器转换成标准化 entries，API 不接收原生规则字符串、远程或二进制规则。

## 已执行证据

- 领域／DTO：`TestRoutingValidationAndStrictDecoding`、`TestRuleSetCanonicalHashAndLineDiagnostics`、`TestRoutingDTOReplacementAndIndexedDiagnostics`、`TestRuleSetDTOHashHistoryAndSafeEntryPointers` 通过。
- 真实 PostgreSQL：`TestPostgresRoutingStorageScopeLatchAndIdempotency`、`TestPostgresRoutingRuleSetCRUDHistoryPaginationAndAudit`、`TestPostgresRoutingRejectsInvalidTargetsEntriesAndUnauditedWrites` 通过，包含 scope／kind／启用状态、乐观修订、历史内容、分页和事务审计。
- 依赖：`TestRoutingAndDNSShareFrozenDependencyClosure` 与查找预算负例通过；冻结构造补齐 2,000 资源边界，不能绕过统一依赖限额。
- 真实 Edge／API／PostgreSQL：[T-049 浏览器验收](../T-049/browser-acceptance.md)证明文本行号、真实 `/entries/1` 拒绝、路由排序及 RuleSet 引用持久化。
- 三内核匹配与正反 Golden 由 T-029 独立验收，管理 API 通过不代替内核运行结论。

父任务整包实库回归通过（80.406 秒），独立七项 Routing／DNS／Preset 实库专项通过（8.863 秒）。最终稳定源码与统一质量结果集中归档于 T-029；本记录不证明 M2 发布／订阅接口已实现。
