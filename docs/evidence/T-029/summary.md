# T-029 编排映射证据索引

本窗口实现版本 `0.2.0-m1-orchestration`，基点 `80ad286a44c77a12c80384e70350951370be788e`，当前未提交、未推送。新增资源／前端与三目标映射属于 M1；没有实现 M2 发布链路。

| 证据 | 已执行结论及边界 |
| --- | --- |
| [拆分清单](work-breakdown.md) | 公共规范化、三个内核、矩阵汇总共五项，累计计入原父任务估算。 |
| [编译与内核运行](compiler-mapping-native.md) | 10 个候选组合均有正反 Golden；72 份配置通过锁定 linux/amd64 检查，72 份损坏配置被拒绝。具体协议参数和变体以表中清单为准，不是任意参数笛卡尔积。 |
| [配置矩阵](native-config-matrix.json) | 每项绑定 fixture、最终配置摘要、CoreBuild ID 及二进制摘要。 |
| [源码清单](native-compiler-source.json) | 原生执行前后源码不变，测试二进制和镜像摘要可追溯。 |
| [手动切换](native-manual-selection.json) | sing-box／Mihomo 的回环控制预设实际切换出口，所选成员不可用时失败，不自动换成员或直连。 |
| [轮询](native-round-robin.json) | Xray／Mihomo 各执行六次连接，每成员三次；所有成员失效，无意外直连。sing-box 按明确能力诊断拒绝轮询。 |
| [低延迟策略](native-latency-best.json) | Mihomo 显式本地 TLS 探测，选中更快成员，全部失效无直连。Xray／sing-box 不能保持健康超时语义时返回字段诊断。 |
| [路由](native-routing.json) | 三目标的 AND／OR、首匹配、后缀本身及点分隔边界、显式 final 均真实运行通过。 |
| [IP 规则与范围变更](native-ip-routing.md) | 60 次已支持模式的路由请求、三个无 IP 规则对照、明确编译诊断通过；原生反例证实 sing-box DNS 失败后不能继续匹配。用户已批准该目标限制，禁止自动降级。 |
| [DNS](dns-mapping.md) | 12 个原生子测试通过，包括顺序、显式 bootstrap／出口、TLS 与无解析回退；精确的内核限制分别列出。 |
| [命名末跳引导](native-chain-bootstrap.json) | Xray 在等价的全 local 方案中先解析 H2 再通过 H1；不同业务 DNS 时明确拒绝不等价配置。 |
| [旧两跳回归](live-chain-regression/report.json) | 三内核共 24 场景通过，涵盖方向交换、节点复用、来源隔离反例、故障不绕行和进程回收；输出见同目录 output.txt。 |
| [最终统一门禁](validation-final/report.json) | 13／13 通过，含 Go vet／race、格式、API／SQLC 漂移、依赖锁、秘密扫描、前端构建、契约和源码稳定性；覆盖最终 IP 目标限制。 |
| [最终真实数据库](validation-final/foundation/report.json) | 57 项 PostgreSQL 测试通过，0 实库跳过；临时数据库容器／网络清理通过。 |
| [编排浏览器](../T-049/browser-acceptance.md) | 真实 Edge／API／PostgreSQL CRUD 4／4；最终生产前端 110 模块构建通过。 |
| [预设启动](../T-022/smoke-report.json) | Linux Compose 37 项通过，五项预设及重启不变性经过真实服务启动验证。 |
| [持久 Runner](../T-041/m1-regression/summary.md) | 真实加密队列／mTLS／锁定内核正反校验／fencing 路径通过，T-041 可复用；不声称已实现 M2 发布。 |

## 状态解释

原生配置检查的 `pass` 只表示相应冻结参数生成的字节可由锁定内核加载。独立运行断言证明表中对应行为，不证明全部协议的远端互通、桌面／移动客户端导入或 arm64 运行。宽泛协议能力目录继续 `unverified`，避免把有限夹具升级成无条件兼容承诺；每个实际执行结果在上述具名证据中单独记录。

`COMPILE_UNMAPPED_FIELD`／`CAPABILITY_UNSUPPORTED` 必须包含资源、字段和 target。原生无法等价表达的 DNS 形状、健康检查或平台参数不会被删字段、回退直连或改为另一策略。候选清单的六协议及 10 种组合未被移出范围。

用户单独批准 sing-box 1.14.0 的 IP 解析限制：该目标的 resolve_for_ip_rules 含有效 IP 条件时拒绝编译；preserve_domain 保留，Xray／Mihomo 的两个模式不缩减。原生反例未标成配置成功，范围决定有独立记录，不以拒绝诊断冒充原范围完成。

## 诊断历史

首次原生检查的失败保留在 `native-compiler-initial-failures.txt`，修正原因见编译记录；首轮统一质量因单文件 gofmt 未完成失败，报告保存在 `quality-initial/report.json`，格式修正后已完整通过。中断的一轮质量没有生成完成报告，不计作成功。Windows 合成 UI 完整运行出现浏览器进程退出，33／34 后剩余一项隔离重跑通过，详见 T-049 记录，不伪称完整命令通过。

最终阶段决定见 [G1 记录](../../reviews/2026-09-10-g1-decision.md)：按用户明确批准范围关闭 G1，M2 未启动。较早的 `validation/` 完整通过记录保留，用于区分 IP 解析补验之前的源码状态；当前交付以 `validation-final/` 为准。最后门禁之后仅更新验收文档、README 和 PLAN 状态，不改应用／测试／契约／生成文件。
