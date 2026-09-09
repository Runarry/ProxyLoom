# ADR-0010：M1 来源身份、受限抓取与两跳链

日期：2026-09-09。状态：按已批准的 M1 下一窗口计划实施。验收以逐任务证据为准。

## 决策

本窗口实施 T-012、T-013、T-017。不实施来源 CRUD、周期刷新、覆盖合并、依赖图、策略组或发布。G1 不关闭，能力目录保持 unverified。

**T-012** 提供纯函数匹配引擎。顺序固定为：来源内 `external_key` → 已确认人工绑定 → 连接内容指纹（重复检测）→ 低置信建议。内容指纹不是稳定身份；名称、地址或协议不足以自动合并。无可靠键的认证变更只产生建议或冲突。`external_key` 只接受调用方提供的显式字段，不从名称、UUID 或密码推导。本窗口不新建 `source_items`／`node_bindings` 表。

**T-013** 提供 `internal/safefetch`。独立 Transport，不继承环境代理；解析后校验 IP 并拨号到已允许地址；TLS SNI 与 HTTP Host 保持原始主机名。默认拒绝 HTTP。私网、回环、链路本地、多播、未指定、IPv4-mapped、CGNAT 与云元数据地址失败关闭。跨 origin 重定向不转发 Authorization。体积、超时与跳转有上限。允许网段仅用于测试夹具，生产调用必须为空。

**T-017** 挂载 Chain HTTP。两端必须是同 scope 的已存在、未删除、已启用具体节点。A→A、三跳、非 node hop 拒绝。交换 hops 只创建链修订，不修改 Node。复用既有 resources／resource_refs；追加迁移只扩展审计动作白名单。

## 验证

匹配与抓取以夹具单测为主；链 CRUD、引用和不修改原节点以真实 PostgreSQL 验收。OpenAPI 仅将已挂载的 chain 操作标为 implemented，并声明契约差异。
