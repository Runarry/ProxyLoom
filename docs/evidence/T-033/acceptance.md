# T-033 持久编译与真实校验

2026-09-12，用户授权的 AI 辅助工程验收。状态：适用检查通过。

compile API Worker 与逐目标 config_validate 原子提交，使用既有租约、fencing 和幂等结果；汇总仅接受全目标 pass。真实 mTLS、锁定 Linux/amd64 三内核、最终完整配置、固定下载字节全链路通过；负例与旧租约拒绝由真实 Runner 回归及事务专项补齐。

共同依据：[M2 证据索引](../T-038/summary.md)、[M2 完整专项](../T-038/m2/report.json)、[全量质量门禁](../T-038/quality/report.json)、[全量实库结果](../T-038/foundation/report.json)、[G2 决策](../../reviews/2026-09-12-g2-decision.md)。

本次为单工作空间、可信管理员和已验 Linux/amd64 构建；不升级未验客户端、arm64 或广泛远端协议能力。
