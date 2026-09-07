# T-001 基线与清单检查证据

日期：2026-09-07。任务状态：`in_review`；人工基线评审：待定；人工审核人：未指定。关联 PLAN T-001，SRS §2／3.2，SDD §1／2；原 FR／AC 编号未改写。

执行环境：仓库 `G:\Projects\ProxyLoom`，Windows PowerShell／pwsh，本地文件检查，不涉及网络节点。最终核对时 `Get-Date -Format o` 返回 `2026-09-07T11:39:19.5839202+08:00`；没有精确逐命令起止记录，不作为性能证据。

起点提交：`8acf714ad4420369b2c52502116fc961daa8c92d`（`git rev-parse HEAD` 实测）。本记录对应其后的 working tree，新代码提交尚未创建；父代理整合与自动检查已完成，人工 review 待完成。最终文件清单见 `../T-002/source-manifest.json`。本任务无内核、适配器或夹具运行，其构建摘要、架构和执行版本不适用；具体开发工具锁由 `deploy/tools.lock.json` 维护。

已执行命令及结果：

```powershell
Get-FileHash docs/requirements_v1.0.md, docs/project_design_v1.0.md -Algorithm SHA256
git rev-parse HEAD
pwsh -NoProfile -File docs/baseline/check-plan.ps1 -WriteTasks
pwsh -NoProfile -File docs/baseline/check-plan.ps1
git diff --check -- docs/PLAN.md docs/baseline docs/adr docs/PLAN.tasks.json docs/evidence/T-001
```

输入字节 SHA-256 实测与 PLAN §14.3 完全一致：

```text
requirements_v1.0.md: 51f77d604b462a88d83e3e45d2de07930e61eed404a641d2be0f708c448d77c7
project_design_v1.0.md: 5846b27b60d6a8d664125200730a83fc40eb74f3f306c1fd0bd50d4658c6f701
PASS: 60 unique sequential IDs; all metadata, direct dependencies and estimates match PLAN; dependency DAG is acyclic.
PASS: M0 tasks=11, estimate=30-51 person-days
PASS: M1 tasks=25, estimate=86-141 person-days
PASS: M2 tasks=10, estimate=34-54 person-days
PASS: M3 tasks=14, estimate=48-78 person-days
PASS: P0 total = 60 tasks, 198-324 person-days.
```

生成后重新执行只读检查，结果一致；已查看 PLAN 实际 diff，`git diff --check` 未报空白错误（Git 提示工作树 LF 将按配置转 CRLF；新增未跟踪文件不在该 diff 检查范围）。这些 PASS 仅指文件摘要和任务清单一致性，不是应用验收、能力验证或 G0 通过。任务清单含每项原始实施内容、逐项验收、主导角色、直接依赖和人日上下界；T-001～003 实施中，其余未开始。

未执行：真实内核配置正负例、受控连通性、绕行反例、客户端导入、双架构与生产验证。外部测试许可未给，后续只以 T-053 自建隔离夹具为默认；详见 `docs/baseline/scope.md`。未决项按角色登记，不能伪造实名确认或审核签字。

最终补记：T-001～003 进入 `in_review`，其余状态保留。任务重生成入口已增加已有文件保护；实际执行覆盖负例，命令拒绝且清单摘要不变。冻结的两份输入文件通过 `.gitattributes` 保留原始字节，避免跨平台换行转换改变基线摘要。Linux 工程检查亦通过任务清单、工具锁与依赖边界验证。
