# T-038 G2 联验与故障验证

2026-09-12，用户授权的 AI 辅助工程验收。状态：适用检查通过。

M2 PostgreSQL 8 项、真实发布／VMess 参数矩阵、真实浏览器 3 项、完整质量入口 13 个步骤、全量 PostgreSQL 65 项（0 跳过）、Windows／Linux race、既有真实 Runner 正反例与 Compose 37 项通过。详细证据、先前失败和验证边界见本目录 summary.md。

共同依据：[M2 证据索引](../T-038/summary.md)、[M2 完整专项](../T-038/m2/report.json)、[全量质量门禁](../T-038/quality/report.json)、[全量实库结果](../T-038/foundation/report.json)、[G2 决策](../../reviews/2026-09-12-g2-decision.md)。

本次为单工作空间、可信管理员和已验 Linux/amd64 构建；不升级未验客户端、arm64 或广泛远端协议能力。
