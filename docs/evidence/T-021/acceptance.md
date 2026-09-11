# T-021 DNS 资源与依赖验收

2026-09-10，基点 `80ad286a44c77a12c80384e70350951370be788e` 的 M1 未提交集成工作树。

追加迁移 000014，DNSProfile 接入加密不可变修订、引用、事务审计、软删除及 ETag CRUD。支持显式 local／固定 IP UDP／HTTPS、bootstrap、本地 resolver ID、有序域名规则和 final resolver。第一项 bootstrap 用于节点域名，HTTPS 单独选择引导项；数组不是隐式并行／回退列表。

## 已执行证据

- `TestDNSReferencesCyclesAndExplicitResolvers`、`TestDNSPatchPreservesSourceAndIndexedSemanticErrors` 通过，覆盖未知／重复 resolver、错误 bootstrap 类型、直接／间接循环、字段路径和严格解码。
- 真实 PostgreSQL 的 `TestPostgresDNSCRUDOrderedRulesReferencesAndAudit`、`TestPostgresDNSRejectsCyclesImplicitResolversAndUnavailableReferences` 通过，包含不可变历史、If-Match、规则顺序、停用／错 kind／跨 scope 出口、域名规则集及循环诊断。
- 通用图将 DNS 出口展开至策略、链和节点，保留禁用规则的编辑引用；冻结输入验证完整根与资源闭包，IP 路由解析必须有显式 DNS 方案。
- [真实浏览器验收](../T-049/browser-acceptance.md)通过 DNS 创建／编辑及真实服务端双边循环 422；诊断不回显测试解析 URL。
- 原生 DNS 的顺序、引导、明确出站和无解析回退由 [T-029 DNS 证据](../T-029/dns-mapping.md)单独给出。无法等价表达的目标组合按具体字段拒绝，不以隐式公共 DNS 补位。

管理 API 仅执行结构和依赖验证，不发起 DNS／HTTP 请求。原生 DNS 行为通过也不能表述为业务 HTTP 连通性或客户端导入通过。最终稳定源码与统一质量结果集中归档于 T-029。
