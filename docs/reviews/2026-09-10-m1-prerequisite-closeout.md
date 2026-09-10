# M1 编排前置评审收口

日期：2026-09-10。性质：用户授权计划内的 AI 辅助工程验收；不是人工签字或 G1 批准。

## 决定

T-008、T-012～T-019、T-048 从 `in_review` 收口为 `done`。本次先核对旧证据，再修复验收中发现的缺口并补验；没有将未关闭的 T-018／T-019 当作已完成依赖。

| 项目 | 收口依据 |
| --- | --- |
| T-008 | [必需检查核实](../evidence/T-008/required-checks-2026-09-10.md)确认此前主分支未保护，现已设置 `checks`／`development-smoke` 为必需、strict、管理员受约束，并读取 GitHub 配置确认。所有 `gh` 调用在沙盒外；未提交、推送或试图合并失败提交。既有质量入口和历史自检证据继续有效。 |
| T-012／013／015／016／017／019／048 | [独立工程复核](../evidence/T-019/m1-review-2026-09-10.md)核对历史实现、证据源码边界和任务验收。复用受审 HEAD 上适用的实库／真实浏览器证据，不外推后续新界面或内核映射。 |
| T-014 | 独立复核发现退避始终从 60 秒计算。现从事务内读取持久退避，连续失败 120→240→cap，成功重置 60；300／600 秒间隔实库反复验证通过。 |
| T-018 | 旧实现仅完成节点／链／策略闭包，不能整项关闭。现补齐路由动作、规则集、DNS 出口、冻结根与预设；缺失、跨 scope、错 kind、停用、排除、循环和 2,000 个资源上限均有检查。独立复核发现冻结构造绕过展开限额，已在构造器／Validate／Schema 同步修复，边界 2,000 成功／2,001 拒绝通过。 |

T-019 关闭的是四策略模型、约束、目标覆盖、CRUD 与修订，不等于三内核四策略全部已验证；实际映射仍归 T-029。T-048 关闭的是节点／来源界面，编排新界面仍归 T-049。

## 本窗口复跑

运行环境 Windows amd64、Go 1.26.0，基点 `80ad286a44c77a12c80384e70350951370be788e` 的未提交集成工作树。数据库是专属隔离 PostgreSQL，凭证只由 `.cache/acceptance-db/` 文件注入，不归档凭证。

- `go test ./internal/capability ./internal/ir ./schemas ./internal/depgraph ./internal/catalog ./internal/apicontract ./internal/server ./api ./migrations`：通过（schemas 为无测试文件包）。
- `go test -mod=readonly -count=1 -timeout=180s ./internal/storage`，必需 PostgreSQL 模式：通过，包耗时 80.406 秒。显式未启用的浏览器入口不计入此次通过项。
- 冻结限额修复后 `go test ./internal/ir ./internal/catalog ./internal/depgraph ./internal/capability`：通过。
- 来源 stable external key＋人工名称覆盖＋上游凭证轮换组合正例由独立验收补齐：同一 ID／binding 保持，新凭证保存，security epoch 恰好递增一次。旧发布访问阻断属于 M2，不虚构发布接口结果。

上述是前置任务的收口依据；最终全量质量、生成漂移、三内核原生检查和编排浏览器结果在新任务证据中分别记录。G1 暂不关闭，待所有阶段门槛实际满足；T-030～T-038 的 M2 发布链路未启动。
