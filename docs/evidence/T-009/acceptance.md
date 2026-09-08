# T-009 六类解析验收

基点 `ae456a293985eb34da78c3bd97fc54ef386bcd82`，本次 M1 工作树；最终统一检查的逐文件摘要见 `m1-validation/quality-source-manifest.json`。

SS、VMess、VLESS、Trojan、SOCKS5、HTTP CONNECT 的约定方言已实现，范围及默认值依据见 `docs/parser-contract.md`。测试包含中文、IPv6、逐部分转义、重复关键字段、缺认证、未知字段隔离、畸形 Base64、两层解码及体积／条数／单行边界。解析诊断与目标兼容状态分别呈现，不据此提升能力 verified。

实现者专项：Go 单元测试、vet、格式检查通过；URI fuzz 15 秒执行 250,584 次，批次 fuzz 15 秒执行 48,550 次，无失败。专项运行计数来自执行记录，未保留原始 fuzz 输出；最终统一质量入口重新运行该包测试，原始输出已归档。夹具使用受控合成输入，没有生产凭证。

共享证据位于 `m1-validation/`：统一质量报告及完整输出、35 项真实 PostgreSQL 测试报告与 JSONL、源码摘要。统一报告 `e7415a05-c637-4e30-9897-3758ad2da03f` 为 source-and-postgres PASS；PostgreSQL 报告 `7f832cd8-7584-4c7f-926e-56890af397db` 无实库测试跳过，容器与网络清理通过。

这些证据是本地工作树验证，不代表该工作树已经提交或运行远端 CI。最终工程收口和平台范围见 `docs/reviews/2026-09-08-m1-input-exit.md`。
