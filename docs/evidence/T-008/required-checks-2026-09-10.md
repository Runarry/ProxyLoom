# T-008 主分支必需检查核实

日期：2026-09-10。检查对象：`Runarry/ProxyLoom` 的 `master`。本地基点：`80ad286a44c77a12c80384e70350951370be788e`。

按用户要求，以下 GitHub CLI 调用全部在沙盒外执行。沙盒内此前的认证失败不代表主机上的 GitHub 凭证失效；沙盒外认证成功。

## 核实与修正

- `gh api repos/Runarry/ProxyLoom/branches/master/protection` 首次返回 HTTP 404，明确为 `Branch not protected`。
- 仓库规则集列表为空，因此当时不存在替代的规则集保护。
- `gh api repos/Runarry/ProxyLoom/commits/master/check-runs` 确认基点上的 `checks`、`development-smoke` 两个 GitHub Actions 检查均为 completed/success，应用 ID 为 15368。
- 依据已批准实施计划的 T-008 要求，使用分支保护 API 将上述两个现有检查设为必需检查，绑定 GitHub Actions 应用，启用 strict（要求检查适用于最新基线）与 enforce_admins。
- 后续 GET 回读确认配置已生效；未要求人工 PR 审批，未限制特定用户，禁止强制推送及分支删除。

## 回读结果

```json
{
  "required_status_checks": {
    "strict": true,
    "checks": [
      {"context": "checks", "app_id": 15368},
      {"context": "development-smoke", "app_id": 15368}
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
```

## 结论与证据边界

必需检查的外部管理配置缺口已消除。没有通过故意合并失败提交验证保护；本记录证明 GitHub 保存并返回了上述保护规则。基点的 CI 成功不得用于证明本次尚未提交的 M1 编排改动已通过远端 CI。T-008 的最终任务状态由本阶段统一验收记录确定。
