# T-001 实施基线

登记日期：2026-09-07。状态：`in_review`，实现与本地自动检查已完成，最终人工基线评审待定；没有已指定的人工审核人或已完成的新代码提交。需求、设计和 PLAN 优先级遵循根目录 AGENTS.md，历史 PSB 编号及 FR／AC 不改写。

| 输入 | SHA-256（原始文件字节） |
| --- | --- |
| `docs/requirements_v1.0.md` | `51f77d604b462a88d83e3e45d2de07930e61eed404a641d2be0f708c448d77c7` |
| `docs/project_design_v1.0.md` | `5846b27b60d6a8d664125200730a83fc40eb74f3f306c1fd0bd50d4658c6f701` |

已执行 `Get-FileHash` 核对，与 PLAN §14.3 一致。需求范围仍为全部 60 项 P0；本轮授权切片仅 WP-01 的 T-001、T-002、T-003，不能解释为 M0 整体完成。正式内核候选锁见 `compat/cores.lock.yaml` 与 ADR-0002（T-023，`in_review`）；开发工具和依赖的具体值集中在 `deploy/tools.lock.json`，本文件不复制或猜测版本。

实施选择见 [ADR](../adr/0001-initial-foundation.md)、[ADR-0002](../adr/0002-core-candidates.md)、[ADR-0003](../adr/0003-compile-and-exec-skeleton.md)、[ADR-0004](../adr/0004-native-emit.md)、[ADR-0005](../adr/0005-live-chain-trust.md)，候选组合和未决事项见 [范围清单](scope.md)。当前能力全部 `unverified`。T-028 已提供 linux/amd64 Trojan TCP/TLS 夹具链路证据，G0 与客户端导入仍待人工评审／未执行。

任务状态唯一机器入口为 `docs/PLAN.tasks.json` 的 `tasks[].status`，阅读入口为 PLAN §12.3–§12.7。当前 `in_review` 为 T-001～T-003、T-023～T-028、T-040、T-053；其余未开始。任务状态不得因拓扑排序自动提升。后续状态修改同时维护 PLAN 实施记录和证据，done 必须满足 PLAN §8.1。运行 `pwsh -NoProfile -File docs/baseline/check-plan.ps1` 检查清单、直接依赖 DAG 与估算；`-WriteTasks` 仅允许在机器清单不存在时初始生成，已有文件会被拒绝覆盖，以保护任务进度和证据历史。
