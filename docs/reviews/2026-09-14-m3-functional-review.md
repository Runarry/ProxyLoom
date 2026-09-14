# M3 功能工程验收记录

本记录核对 M3 第一、二批及系统页面、保留治理的实现与自动化证据。它不替代 G3 的双架构、持续容量、恢复调度和最终供应链结论，也不授予公开发布许可。

结论：下列 8 项在其既定 P0 范围内通过工程验收，任务状态记为 done；其余 6 项 M3 父任务维持实施中。历史分项证据中的“尚未关闭”描述保留其当时语境，以本记录和当前 PLAN 状态为准。

| 任务 | 已实现的验收行为 | 可追溯证据 |
| --- | --- | --- |
| T-043 | 原子整批预留、并发不超卖、attempt 独立结算、跨 UTC 日归属、幂等及取消/失联保守计量 | [预算](../evidence/T-043/summary.md)、[当前完整实库](../evidence/T-057/final-postgres/report.json) |
| T-042 | 登记 HTTP(S) 目标、冻结批准地址、真实节点与完整两跳、响应断言、inconclusive、生产出站隔离 | [联网](../evidence/T-042/summary.md)、[部署](../evidence/T-054/summary.md) |
| T-044 | 应用读取字节/单调时长双上限、短样本拒绝吞吐结论、样本与截断原因、下载不自动重试 | [原生负例](../evidence/T-042/extended-native/report.json)、[完整 M3](../evidence/T-045/durable-restarts/report.json) |
| T-045 | 取消有界回收、旧租约拒绝、日志和进程清理、监督进程强杀、API/Runner/数据库实际重启后不重复下载 | [故障记录](../evidence/T-045/summary.md)、[完整 Linux 进程](../evidence/T-045/final-linux-processes/report.json) |
| T-046 | 不可变测试历史、完整依赖修订绑定、分页、样本聚合、事件重放和快照恢复 | [完整 M3](../evidence/T-045/durable-restarts/report.json)、[当前实库](../evidence/T-057/final-postgres/report.json) |
| T-051 | 批量节点/链测试、任务详情、取消、刷新、断网重连、移动/键盘查看、历史标记与失败解释 | [当前浏览器](../evidence/T-052/final-browser/report.json) |
| T-052 | 概览、构建停用、版本状态、设置白名单及修订冲突、审计筛选和中文页面 | [系统页面](../evidence/T-052/summary.md)、[当前浏览器](../evidence/T-052/final-browser/report.json) |
| T-055 | 低基数独立认证指标、故障提示、审计、默认保留、引用保护、清理暂停与有限事务 | [保留治理](../evidence/T-055/summary.md)、[当前实库](../evidence/T-057/final-postgres/report.json)、[操作手册](../self-hosting.md) |

当前源码质量入口 [9cde9bf8](../evidence/T-057/source-quality/report.json)通过 Go race、格式/静态检查、API 和 Schema 契约、生成类型、前端构建、锁定版本及秘密扫描。Windows 跳过的 5 项 POSIX shell 自测另在无网络、只读挂载的 [Linux 容器中全部通过](../evidence/T-057/posix-init/report.json)，含缺少口令拒绝和正确口令传递；Linux 实际部署与进程隔离另有证据。

联网自动验收使用隔离合成对象与受控服务；72 个原生配置正反例和 Trojan 两跳回归不扩大已批准能力范围。测试功能不改变发布门槛。arm64 保持运行未验，未验证客户端保持未验证。
