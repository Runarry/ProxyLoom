# ADR-0007：M1 资源、秘密与 HTTP 契约基座

日期：2026-09-08。关联 T-004、T-005、T-007。状态：按用户批准的首窗方案实施；测试及评审状态以任务证据为准。

前置：用户已批准限定 linux/amd64 Trojan TCP/TLS 原型及临时容器测试 CA 方案，关闭 G0 并授权启动三项任务、更新 PLAN。批准记录见 `docs/reviews/2026-09-08-g0-decision.md`。没有扩大能力 verified 或其他平台验收范围。

## 决定

1. 领域仓储契约位于 catalog，不导入 pgx；PostgreSQL 实现位于 storage，使用锁定 sqlc 生成的 pgx/v5 查询。Node／Chain 使用现有类型化 IR，历史修订加密保存完整 metadata＋payload。当前资源和标签表仅维护可查询的当前索引。
2. 固定 scope 先锁、资源按 ID 锁、修订及引用随后写入。一个资源变更事务只推进一次 catalog_revision；失败回滚与幂等重放不推进。数据库约束补齐无头提交、引用 scope/kind、修订连续性、不可变历史和必要最小权限。
3. AES-256-GCM 业务载荷和 DEK 包装分离。AAD 绑定 scope、表、对象、修订、Schema 版本，两层区分用途；包装额外绑定 key_id 和载荷摘要。资源历史密文不可变，主密钥重包以包装版本 CAS 更新独立记录，不产生业务修订或推进安全 epoch。
4. 主密钥新增显式 `PROXYLOOM_MASTER_KEY_ID`；已有三个独立 32 字节秘密文件继续使用。可选 `PROXYLOOM_OLD_MASTER_KEYS_FILE` 是旧 key_id 到绝对文件路径的严格 JSON 对象，最多 15 个旧密钥。配置丢失、重复、篡改和未知版本失败关闭，不能自动生成替代密钥。开发 Compose 固定非秘密 ID `dev-master-v1`；该 ID 不改变现有文件的密钥值。
5. HTTP DTO 和内部 IR 分离。秘密补丁缺省保留、null 清除、字符串替换，合并后验证完整协议认证；响应只返回配置状态。HTTP revision、epoch、generation、lease_seq 和事件 seq 等 int64 计数使用规范十进制字符串；有界 attempt 等仍为数字。该首次 API wire 冻结保留 int64 精度，不更改 IR、数据库或编译接口的整数类型；ETag 仍为 `"r<N>"`。
6. 持久幂等以主体＋固定路由＋key 为唯一键，请求采用用途隔离 HMAC。回调通过同一事务的资源能力写入，持久化仅保存可验证的状态／ID／修订凭据，不保存任意响应体或一次性令牌明文。
7. OpenAPI 3.1 是 HTTP 契约来源，openapi-typescript 生成前端类型，Go DTO 通过实际 Schema 验证。管理 Cookie、Runner mTLS、订阅路径令牌分别鉴权。OpenAPI 没有 path 类型的 apiKey scheme，因此订阅准确采用必填 token 路径参数和明确的鉴权扩展约定，不虚构 Authorization header；security 空数组不表示业务允许匿名取配置。
8. 本窗业务功能通过领域调用及测试专用 HTTP 路由集成，不注册无鉴权节点 CRUD、登录或 Runner 业务处理器。T-006／T-008 与完整 G1 仍按原计划接续。

## 验证与代价

新增 sqlc 工具和 OpenAPI 类型生成依赖均锁定精确版本；sqlc 下载归档及解压后二进制分别校验摘要，生成漂移检查写临时目录，不自动修改受检源码。首次运行需取回指定工具；后续可以复用经过摘要校验的缓存。

真实 PostgreSQL 验收使用独立命名、带所有权标签的临时容器、独立 bridge 网络与 tmpfs 数据库，只挂载新生成的测试凭证；不复用开发卷或真实节点。端口仅发布在宿主 127.0.0.1，并在测试前核验绑定。采用 bridge 是为了让 Windows 主机测试程序连接数据库；Docker Desktop 的 internal 网络在首轮实测中未分配宿主端口，该失败和成功清理记录保留。测试环境不等同生产 Runner 的出站隔离。验收脚本要求数据库测试实际执行且不得 skip，清理失败不能报告整体通过。其通过范围与代码摘要另存各任务证据。

主密钥轮换需要同时保留仍被历史包装引用的旧密钥；包装迁移不意味着旧密钥可以立即销毁。API 十进制计数需要显式 DTO 转换，前端不能把它们隐式转换为 JavaScript number。完整 F1 还包括后续 T-006／T-039 的认证与持久任务实现，本窗不宣称替代其验收。
