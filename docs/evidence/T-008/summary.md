# T-008 持续质量检查与测试入口

日期：2026-09-08。状态：实现和本地自动验收完成，`in_review`。远端合并保护尚未核实，不宣称完整 G1。

## 交付

统一 `scripts/check-quality.mjs` 入口串接锁定工具、候选／暂存区秘密扫描、Node 自检、Go 格式／依赖／vet／race、sqlc、前端类型／构建、API 生成漂移、契约差异及实际 PostgreSQL 基座／身份验收。CI 的 `checks` 使用同一完整入口，`development-smoke` 保留为独立作业；新增 PR 模板和限定附件暂存器。

API／Schema 变动需要精确摘要与 JSON Pointer 声明，配套文档、夹具及生成类型同时变化；旧迁移改动直接拒绝。T-006 的实际差异登记于 `compat/contract-changes/T-006.json`。扫描例外仅允许审阅过的具体文件、规则和整行摘要；运行输出与已登记生成秘密不接受此例外。规则覆盖实际 `sub_<public_id>.<random>` 令牌形式。

## 最终验收

- **完整统一入口 PASS**：[report.json](validation/report.json)、[完整输出](validation/quality-full.txt)。运行 `10f55cc4-ec3f-4b00-96fa-74eeb1db21a5`，13 步均 pass，源码开始／结束一致。命令 `node scripts/check-quality.mjs`，本机使用仓库 `.cache/go-build`、`.cache/gomod`，Docker 管道使用验收所需权限。
- **46 项 Node 检查：41 pass、5 Windows 平台 skip**。新增质量负例实际运行失败 Node 子进程、API 生成漂移 CLI、契约删除／约束／缺配套文件、暂存区秘密、附件污染等，证明失败不会变成通过。5 项 POSIX 初始化负例另在 Linux 检查镜像中执行，与诊断测试合计 **11 pass、0 skip**，见下面平台补验记录。
- **20 组实库测试 PASS、0 skip**：[full-profile PostgreSQL report](validation/foundation/report.json)，包含 11 组身份相关测试。独立容器和网络清理均 pass；Go race 阶段无 DSN 的 skip 不替代此实库步骤。
- **Linux 全工程 vet/race PASS，Linux API Compose 31 检查和清理 PASS**：复用 [T-006 验收](../T-006/summary.md)。前端构建和 API/sqlc 再生成一致性已包含在最终完整入口。
- **真实证据暂存 PASS**：[manifest](validation/staged-evidence-manifest.json)。实际 collector 向新的 `.cache/quality-upload-final/` 暂存 12 个扫描后的允许文件，未包含凭证。已有 `.cache/quality-upload/` 不复用，避免旧文件混入。

最终完整入口绑定基点 `1ff4c094d9d268c8afae82a6987ba216892b2a46` 上的源码，见 [source-manifest.json](validation/source-manifest.json)。随后仅收尾任务状态／README 与证据文档，代码和测试未改变；对应最终复核记录见 `final-reconciliation.json`。

### POSIX 补验

已执行并退出 0：

```text
docker run --rm --network none --entrypoint node proxyloom-check:t006-t008 --test scripts/postgres-init.test.mjs scripts/foundation-report.test.mjs
```

使用已通过工程检查的锁定 Linux 镜像，无网络或主机挂载。11 项通过，无 skip；其中不可读、空运行密码及空迁移密码均在 psql 调用前失败，合法输入才导出到受控测试进程。此处为实际执行记录，不冒充新工作树已在远端 CI 运行。

## 早期失败与收口

- 非交互 pnpm 冻结安装先因模块目录重建确认失败：[记录](early-failures/noninteractive-install.json)。仅对该安装子进程显式设 CI=true 后解决，保留锁文件，不自动更新版本。
- 默认 Go 缓存路径受限导致源码检查失败：[记录](early-failures/go-cache-permission.json)。改用仓库缓存后通过，不关闭依赖校验。
- 自检发现 Node 内部测试上下文继承可能影响子进程模式，执行独立检查前移除 NODE_TEST_CONTEXT，并用实际失败测试验证非零退出。
- 补充最终源码清单读取失败的负例，确保已经成功的早期步骤不能使最终异常被记为 pass。

GitHub 主分支保护只读查询返回 HTTP 401，无法核实 `checks`、`development-smoke` 是否已配置为强制合并条件。本次不更改远端规则、不声称新 CI 已执行、不自动标任务 done 或能力 verified；后续仓库管理员可据固定作业名完成该远端核实。
