# T-017 两跳链领域与接口

挂载 `GET/POST /api/v1/chains` 与 `GET/PATCH/DELETE /api/v1/chains/{id}`。两端必须是同 scope、未删除、已启用的具体节点。A→A 由契约 `uniqueItems` 拒绝（400）。缺失或停用 hop 返回 422。A→B 与 B→A 是独立资源。交换 hops 只增加链修订；hop 节点修订与 security_epoch 不变。软删除链不级联删除节点。节点 `references` 可看到链引用。追加迁移 `000008_chain_audit.up.sql` 扩展审计动作，不改写 000001–000007。

验证：真实 PostgreSQL `TestPostgresChainCRUDSwapReuseAndHopRejection` 通过，foundation 运行 `b2068e8e-45e0-4d18-bb10-f44e23160072`。OpenAPI 对应操作标为 implemented；契约声明 `compat/contract-changes/T-017.json`。未实现编排 UI、依赖图或 URI 导出。
