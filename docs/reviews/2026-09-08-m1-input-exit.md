# M1 节点输入与持久校验窗口退出记录

结论：用户授权的六项完整任务已完成本窗口工程验收，T-048 节点／本地导入切片通过；累计 21 项 done。T-008 继续 in_review，T-048 整项继续 in_progress，其他 37 项 not_started。G1 不关闭，能力目录不提升 verified。

本次依据用户“PLEASE IMPLEMENT THIS PLAN”及其完整 M1 方案实施。基点为 `ae456a293985eb34da78c3bd97fc54ef386bcd82`，交付对象为当前未提交工作树。评审由主代理整合实现者专项、独立浏览器验收和独立隔离审查完成；这是 AI 辅助工程评审，没有虚构其他人的审阅、签字或发布批准。

| 任务 | 退出结果 | 证据 |
| --- | --- | --- |
| T-006 | done，限定身份工程评审收口 | [进入记录](2026-09-08-m1-input-entry.md) |
| T-009 | done，六类 URI 方言、诊断和限额 | [解析验收](../evidence/T-009/acceptance.md) |
| T-010 | done，持久预览、5,000 条原子确认及清理 | [导入验收](../evidence/T-010/acceptance.md) |
| T-011 | done，节点管理、克隆、不可变修订、安全 epoch | [节点验收](../evidence/T-011/acceptance.md) |
| T-039 | done，持久任务、租约、取消、重试及父批次 | [队列验收](../evidence/T-039/acceptance.md) |
| T-041 | done，注册 mTLS、构建核验、隔离配置校验及内部提交服务 | [执行器验收](../evidence/T-041/acceptance.md) |
| T-047 | done，初始化／认证、会话、路由和中文基础体验 | [独立前端验收](../evidence/T-047/independent-acceptance.md) |
| T-048 | 切片通过，整项 in_progress | [独立节点／导入界面验收](../evidence/T-048/independent-acceptance.md) |

实现复用稳定 IR、资源事务、信封加密与认证服务；新增 000005～000007 迁移而不改写旧迁移。三项前置契约修正已同步 OpenAPI、生成类型、夹具和文档：T-010 实际依赖、200 条预览分页／5,000 条提交容量、网络任务才要求 quota_reservation_id。克隆和可选 TLS 清除契约一并登记。来源操作明确拒绝；公开 API 不接收任意原生配置。

## 最终质量证据

- [统一质量报告](../evidence/T-009/m1-validation/quality-report.json)：source-and-postgres PASS，运行内源码稳定。格式、静态分析、Go race、前端类型与构建、锁文件、契约生成、秘密扫描和脚本自检通过。脚本自检 Windows 平台为 41 通过、5 平台跳过，未将跳过算通过。
- [真实 PostgreSQL 报告](../evidence/T-009/m1-validation/postgres-report.json)：35 项通过，无实库用例跳过；包含新节点／导入／队列与原有身份／加密／资源回归。测试使用独立角色与临时数据库，容器及网络清理通过。
- [Linux 工程输出](../evidence/T-009/m1-validation/linux-engineering-output.txt)：锁定 Go 1.26.0 的 linux/amd64 镜像完整 vet、race 和工程检查通过。该次 race 无实库 DSN，数据库用例不计入其通过范围；实库结论来自上一项。
- [Compose 报告](../evidence/T-009/m1-validation/compose-smoke-report.json)：32 项通过，重新构建 Linux API／Runner 和生产前端，验证认证、迁移并发、故障关闭、重启及恢复、网络边界、优雅退出与无秘密日志，专属测试资源清理通过。这是默认开发 Compose，Runner 为无传输的空闲模式；实际 mTLS／三内核完整路径另由 T-041 验证，不混称为同一次部署测试。
- T-041 专用 Linux 隔离测试实际执行三内核合法／非法配置、网络与文件拒绝、资源限制、取消清理。真实加密 PostgreSQL → mTLS → 锁定内核 → fenced 回传路径通过。报告绑定源码及测试二进制摘要。
- 真实 Chrome／API／PostgreSQL 六项 E2E 通过；新增删除断言后单项复验通过。另有七项前端单元、十项受控响应 UI 检查和真实数据库断网／恢复验收。真实浏览器使用 205 条跨页导入；5,000 条容量在真实后端验证，未伪称浏览器执行 5,000 条。

验收发现的实际缺陷均在冻结前修复：新增审计逆向锁顺序、数据库半断开管理请求无响应、可选 TLS 清除表达缺口、可执行文件路径替换窗口、执行器运行时资源故障分类及虚拟地址空间波动。失败、测试工具修正和最终复验均在逐项记录中说明，没有省略受阻／跳过范围。

统一检查后仅同步证据和 PLAN 状态；[最终摘要核对](../evidence/T-009/m1-validation/final-source-reconciliation.json) 逐文件对照统一检查源码清单，确认应用、迁移、契约、生成文件和验证脚本没有继续变更。最终计划一致性、契约声明及秘密扫描再次执行；不为文档状态修改重复全部真实测试。

## 保留边界与下一窗口

基点 [Engineering checks 34233250087](https://github.com/Runarry/ProxyLoom/actions/runs/34233250087) 两项工作流成功，但分支保护 API 返回 401，无法证明强制合并检查配置。因此 T-008 保持 in_review；本次工作树没有提交或远端 CI 结果。

Linux arm64 只完成交叉构建，未真实执行；生产双架构、证书生命周期、完整 P0 兼容／安全验收仍属后续工作。节点安全 epoch 的持久化不等于旧订阅下载阻断已完成，后者随 M2 读取路径验收。配置校验不执行网络探测，不关闭 G1，不启动发布或测速。

下一步沿原计划推进 T-012／T-013 → T-014 → T-015 和 T-017／T-018 → T-019～T-022，再补 T-029／T-030 及依赖项。T-048 的来源刷新、覆盖、冲突合并等待相应后端；任务中心留在 T-051。原任务范围和人日估算保留，实际投入未采集，不用本次代理运行时长冒充人日。
