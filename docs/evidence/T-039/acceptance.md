# T-039 持久任务验收

新增迁移 000005 和独立 jobs 服务；协议 DTO 位于 runnerprotocol，不携带管理服务或数据库依赖。统一源码摘要及实库原始输出见 `../T-009/m1-validation/`。

七项真实 PostgreSQL 队列／HTTP 用例全部通过：12 个竞争领取者、加密载荷和进程重建恢复、attempt／lease_seq fencing、旧心跳及结果拒绝、重复结果回执、事务回调失败回滚、到期恢复、父批次取消与聚合、事件／SSE 续接、槽位限制、能力类型过滤、配置错误不重试和基础设施最多一次重试。HTTP 用例包含 Cookie、CSRF、作用域、游标、版本、会话撤销及数据库失败关闭。

Worker 和纯协议单元测试、vet 与边界检查通过。默认租约三十秒，心跳五秒。取消与终态持久化，以 PostgreSQL 为事实源；API Worker 仅领取 import_parse，本窗口 Runner 仅领取 config_validate。

纯配置校验 max_bytes／settled_bytes 为零，不要求流量预留；网络任务预留要求留在协议中，T-043 网络预算实现没有提前标记完成。真实 mTLS 和三内核队列路径另见 `../T-041/acceptance.md`。
