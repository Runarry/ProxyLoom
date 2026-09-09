# T-019 来源闭环与策略组窗口验收

日期：2026-09-10。基点：`29e29c82f99a50e3ecb2fbe6343bfb6c1310246c`；新增工作区代码尚未提交。状态 `in_review`，不批准 G1、不改变能力验证状态。

## 实现

策略组类型化 IR、覆盖纯合并、CRUD、分页、If-Match、审计、不可变修订与完整 Node／Chain 引用闭包已接入。冻结输入保留策略载荷；当前三目标遇到策略返回 CAPABILITY_UNSUPPORTED，不静默丢弃。

来源闭环与导出契约见 `docs/m1-source-policy-contract.md`，共享追加迁移 000011／000012、OpenAPI、Schema、生成类型与 sqlc 输出。

## 验证记录

- 已执行：策略相关 IR／catalog／depgraph／compiler／apicontract／storage／server／schemas 单测；无数据库环境中的实库 skip 不作为通过证据。
- 已执行：Base64 导出及重认证、历史修订、不可表达字段诊断的真实 PostgreSQL 检查。
- 已执行：锁定三内核 linux/amd64 配置正负校验与启动取消，见 [compile-exec.txt](validation/compile-exec.txt)。仅回归既有原型，不证明策略映射已实现。
- **最终统一入口 PASS**：`node scripts/check-quality.mjs`，运行 `acb505f3-a569-47d3-a3f0-eb7c86e49c6e`，13 步全部通过，包含 Go 格式／vet／race、依赖锁、前端类型与构建、API 生成漂移、精确契约声明及源码开始／结束一致性。见 [report.json](validation/report.json)、[源码清单](validation/source-manifest.json) 与 [完整输出](validation/quality-output.txt)。
- **真实 PostgreSQL PASS**：运行 `518d57ed-71ab-45fb-90e4-a7f75f4c0950`，48 项通过、0 项实库 skip，包括 3 项策略组、5 项来源预览、新增 Base64 导出及既有 Source／Override 回归。见 [实库报告](validation/foundation/report.json)、[测试事件](validation/foundation/go-test.jsonl)。容器与网络清理通过。
- **真实浏览器 PASS**：Chrome、真实 API／Worker、临时 PostgreSQL 与本地许可来源夹具，4／4 通过；运行前后源码一致。见 [独立验收](../T-048/source-window/independent-acceptance.md)。此前前端专项 11 项来源 UI、10 项既有 UI 与 7 项表单测试通过。
- 统一脚本自测为 41 pass、5 个既有 POSIX 专项在 Windows skip；这些脚本未在本窗口修改，不把 skip 声称为 Linux 补验。浏览器测试入口默认 skip，单独通过显式启用的真实验收运行。

## 缺陷与重跑记录

- 显式 `external_key` 原先被 URI／VMess 解析器当作不支持字段拒绝，已修复为校验后的隔离元数据，并通过解析正负例及真实浏览器稳定 ID 更新验证。
- 空 2xx 来源响应原先被错误归为可重试服务故障，已改为 INVALID_CONFIG，避免占用下一次刷新；新来源专项回归通过。
- 首次统一运行 `ba6d6eb8-eba4-434d-b839-9f252fcc46ad` 的各功能步骤通过，但执行期间浏览器测试文件有修正，最终源码一致性失败。保留 [早期报告](validation/early-source-change-report.json)，待所有写入停止后完整重跑并通过；不将首次总结果记为通过。

## 范围

T-017／T-018 的既有验收只覆盖链与通用展开；本窗口补策略及来源交互专项。实际人日未采集，原估算不变。远端 CI 与 T-008 主分支强制检查未在本窗口核实。
