# T-018 依赖图与展开校验

实现 `internal/depgraph.Expand`：从根资源沿引用闭包展开，默认限额 2000。缺失、错 kind、循环（`DEPENDENCY_CYCLE`）、超限与 `exclude` 命中必要依赖（`DEPENDENCY_EXCLUDED`）均写入诊断并带字段路径。Walker 可通过 `Options.Refs` 注入。链 HTTP 在 hop 存在性／启用校验之后，用草稿 Chain 与 hop 节点做一次 Expand。本窗口不挂载独立 expand HTTP，不展开策略／路由／DNS（那些资源尚未落地）。

验证：`TestExpandChainIncludesHopsAndRejectsMissingExcludeAndCycles` 覆盖正例与负例；链 PostgreSQL `TestPostgresChainCRUDSwapReuseAndHopRejection` 继续拒绝缺失／停用 hop。foundation 运行 `638c3547-f8af-4f57-ab6f-2b58ab4d7fb8`。未关闭 G1，未标记能力 verified。
