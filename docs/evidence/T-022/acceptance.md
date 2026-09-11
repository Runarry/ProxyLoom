# T-022 审核预设与启动验收

2026-09-10，基点 `80ad286a44c77a12c80384e70350951370be788e` 的 M1 未提交集成工作树。

提供三个不可编辑 Linux 文件预设（Xray JSON、sing-box JSON、Mihomo YAML），以及用户补充批准的两个回环控制变体。全部固定 SOCKS `127.0.0.1:1080`、`dns_mode=profile`、`import_method=file`；控制变体分别为 sing-box `127.0.0.1:17812`、Mihomo `127.0.0.1:17813`。旧三个预设不自动开启控制。

## 实现与验证

- 正式 `serve` 在监听管理 API 前初始化单工作空间和预设；GET 列表只读。稳定 ID 包含 scope／family／variant，重复及并发初始化不新增修订，旧三项升级为五项时保持原字节。
- 公共资源修改路径和数据库触发器均拒绝预设改名、停用、软删除及修订替换。数据仍按现有不可变修订加密保存。
- 冻结 IR／OpenAPI 同时限定五项允许的监听和格式；关闭控制时不能携带被忽略的 listen／port。控制地址、其他端口、脚本、任意字段、原生扩展、TUN、下载与系统标签覆盖不开放。
- `TestPostgresClientPresetsProvisionOnceReadOnlyAndImmutable` 和 `TestPostgresClientPresetsUpgradePreservesOriginalThree` 通过；包括并发初始化、分页／过滤、密文、历史值及绕过领域层的数据库变更拒绝。
- `TestReviewedControlPresetsUseExactFamilyEndpoints`、`TestDisabledControlPresetsStillRequireReviewedListener`、混合原生字段拒绝及冻结深复制测试通过。
- [真实浏览器验收](../T-049/browser-acceptance.md)确认五项只读；[三内核原生验收](../T-029/)另验证加载与控制切换。桌面／移动客户端和 arm64 不由本验收外推。

## Docker Compose 实际启动

沙盒外执行 `pwsh -NoProfile -File scripts/smoke.ps1 -Build -Port 18093` 构建专属项目；随后修正测试解析并使用同一构建执行 `pwsh -NoProfile -File scripts/smoke.ps1 -Port 18093`。

最终 [smoke-report.json](smoke-report.json)：`completed=true`、`cleanup=pass`，linux/amd64。全部 37 项通过，包含初始化后五项预设、原三项关闭／两项控制、API 重启后 ID／修订／值相同、认证与数据库故障恢复、Runner 隔离、日志不含开发秘密。专属容器／网络／卷已清理，没有删除现有开发环境。

首轮 [smoke-initial-report.json](smoke-initial-report.json)保留：36 项功能断言通过，最后日志断言失败。原因是新增烟测把预设数组误交给只适用于认证对象的 `Read-AuthResult`；PowerShell 将五个空 `csrf_token` 投影转为四个空格，误入秘密列表并匹配日志缩进。用合成五空对象复现确认。修正为直接解析预设响应，不改变应用或削弱秘密检查；重跑通过。

审核通过只代表这些结构化预设获准。界面明确区分“仅验证内核加载”、运行时行为和客户端导入，未验证项仍为 `unverified`。
