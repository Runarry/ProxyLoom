# T-048 开发 Compose 网页补充测试 — 2026-09-10

## 结论与证据范围

本轮补充当前提交在开发 Compose 中的网页操作证据，涉及 T-048 节点／导入界面及 T-047 通用交互。已执行项目未发现阻断性产品故障；本轮不是完整 Web 验收，T-048 保持 `in_review`，G1 和内核能力状态不变。

本记录根据同一任务中已完成的构建输出、浏览器可访问性快照、可见 HTTP 错误和独立测试代理结果补录。文档更新时未重跑业务测试；没有另存本轮完整日志、截图或不可变测试附件。合成 UI 的忽略目录 `proxyloom-web/test-results/ui/.last-run.json` 在补录时为 `passed`，但该文件可被后续运行覆盖，不能单独证明测试数量或覆盖范围。

- 受测 Git HEAD：`62796f57e35965b005016dcef9adc2546d77cf0d`；测试时受版本控制的工作树干净。
- 环境：Windows、Docker Desktop Linux 引擎 29.7.2、Docker Compose v5.5.1；开发入口 `http://127.0.0.1:8080`，浏览器时区 Asia/Singapore。
- 手工网页执行面为 Codex 内置浏览器与真实 API／PostgreSQL；独立合成 UI 回归使用系统 Chrome、Playwright 1.58.2。
- 用户先完成管理员初始化和基础节点创建。本轮沿用现有登录会话；原有两节点 `manual`、`manual-trojan` 未被修改。
- 只记录状态、名称、修订和诊断，不归档节点秘密、管理员密码、setup token、Cookie 或导入原文。

## Compose 构建与启动

仓库根执行：

```powershell
pwsh -NoProfile -File scripts/dev-init.ps1
docker compose -f deploy/compose.dev.yaml up --build -d --wait
docker compose -f deploy/compose.dev.yaml ps --all
```

已有开发秘密组被保留。API 和 Runner 镜像构建完成，构建期间前端类型检查与 Vite 构建通过。后续容器状态确认 API、PostgreSQL、Runner 均为 `healthy`；迁移容器正常退出 `0`，输出 `applied=12, pending=0, latest=12, current=true`。

初始化前 `/healthz`、`/readyz`、`/setup` 为 HTTP 200，未登录 `/api/v1/auth/me` 为 HTTP 401。Runner 日志为 `idle`、`task_execution=false`，只证明空闲开发进程健康，不证明实际内核任务执行。

## 本轮真实网页观察

| 结果 | 操作 | 可见结果与边界 |
| --- | --- | --- |
| PASS | 节点详情、修订、引用 | `manual` 展示 r2、安全代次 1；历史有 r1／r2；引用页展示空状态。未检验非空引用。 |
| PASS（限定） | 查看并清除临时秘密 | 查看后出现临时对话框，点击“隐藏并清除”后关闭并返回配置页。沿用用户已有近期重认证；没有在本轮输入管理员密码，不能据此声称重认证门槛已重新验证。未检查浏览器存储、60 秒或隐藏标签页清理。 |
| PASS | 服务端克隆 | 从 `manual` 创建 `webtest-manual-clone-20260910`，新 ID、r1；原节点仍为 r2。 |
| PASS（限定） | 编辑保留秘密、添加标签 | UUID 控件保持“保留已保存的值”，添加 `manual, webtest` 后保存为 r2，安全代次仍为 1。证明保存与保留模式可用；未抓取请求体或再次逐字比较秘密。 |
| PASS | 拒绝清空必填 UUID | 对副本选择“明确清除（null）”并保存，返回 HTTP 422 `VALIDATION_FAILED`，字段定位 `/auth/uuid`；取消后详情仍为 r2。 |
| PASS | 协议筛选 | 选择 VLESS 并点击筛选，显示原 VLESS 节点和副本共两项，排除 Trojan。未覆盖其他筛选和列表翻页。 |
| PASS（单项） | 批量入口标签／启停 | 只提交测试副本：添加 `batch-webtest` 到 r3，停用到 r4，再启用到 r5；每次显示“成功 1，未成功 0”。曾验证两项勾选，但提交前取消原节点选择，未验证多项事务结果、跨页批量或部分失败。 |
| PASS（预览） | 自动识别混合 URI 输入 | 三条输入生成三候选：Trojan 已有匹配；VLESS 因副本同连接而显示 `IMPORT_DUPLICATE_EXISTING`；无效行显示 `IMPORT_INVALID_URI` 且操作禁用。默认全部跳过、提交按钮禁用。未提交候选，未新增正式节点。 |
| PASS（表单） | 来源空状态与必填校验 | 来源列表为空；空表单点击创建后显示“请填写此字段”。取消后仍为空列表，没有创建或刷新来源。 |
| PASS（观察时段） | 浏览器控制台 | 所选标签页捕获的 warning／error 查询结果为空。不是全站或所有浏览器无错误的保证。 |

## 同轮独立自动化回归

以下命令在 `proxyloom-web/` 中执行；结果来自同轮独立测试代理，不等同于上述真实 Compose 页面覆盖。

| 结果 | 命令 | 证据摘要 |
| --- | --- | --- |
| PASS | `pnpm test` | 7／7 表单与秘密语义单元测试通过。 |
| PASS | `pnpm typecheck` | `vue-tsc --noEmit` 通过。 |
| PASS | `pnpm check:api` | 类型与本地 OpenAPI 重新生成结果一致。 |
| PASS | `$env:PROXYLOOM_E2E_BROWSER='chrome'; pnpm test:ui` | 21／21 通过，19 个合成 API 浏览器用例和 2 个纯函数用例，19.4 秒。覆盖包括修订冲突、来源／覆盖交互、导出请求、导入分页和限制；不能外推为真实后端通过。 |
| 预期拒绝；真实套件 NOT RUN | 未设置真实 E2E 环境变量时执行 `pnpm test:e2e` | 退出码 1，配置要求真实 API 地址和合成管理员；没有执行真实业务用例。 |
| 仅枚举；NOT RUN | 提供虚构配置后执行 `pnpm test:e2e -- --list` | 列出两个文件、10 个用例，不启动浏览器、不发业务请求，不计为测试通过。 |

首次直接运行 `pnpm test:ui` 时，自带 Chromium 未安装，19 个浏览器用例启动失败、2 个纯函数用例通过。使用项目支持的系统 Chrome 重跑后全部通过；保留该环境失败说明，不将首次运行写成通过。

## 未执行项与既有证据

本轮未执行真实 412 编辑冲突、导入提交／幂等／文件和 Base64 导入、导出下载、删除、多节点批量、秘密自动清理、登录／退出回归、来源成功刷新／差异确认／覆盖恢复、链和策略组 API。未运行完整质量入口、数据库故障烟测、真实内核或远端 CI。

这不意味着项目没有对应历史证据：[节点／导入独立验收](independent-acceptance.md) 和 [来源闭环独立验收](source-window/independent-acceptance.md) 已记录各自代码状态下的真实 API／临时 PostgreSQL／Chrome 验证。本轮不把历史结果重新标为当前提交重跑通过。

开发 Compose 来源页当时为空，且没有配置可控 HTTPS 来源，所以本轮只完成表单验证。来源自动化另有 `TestSourceWindowBrowserAcceptance`：在独立测试环境中启动回环来源夹具并注入专用许可规则；不需要用户提供外部 HTTPS 地址。其真实执行和环境回收方法见 [前端验证说明](../../../proxyloom-web/README.md#验证)；本轮未调用该入口。

## 文档问题与观察项

- 前端 README 将来源刷新／覆盖写成后续切片，且遗漏 `PROXYLOOM_E2E_SOURCE_FIXTURE_URL`。来源 spec 只接受专用回环夹具，不可用任意 HTTPS 订阅替代。该说明已在本次文档更新中纠正，未更改测试或应用逻辑。
- `source-window.spec.ts` 自身只撤销会话，没有逐个清理所有来源／绑定节点；应由临时数据库入口整体回收。原 README 对整个真实套件“结束时删除合成节点”及登录次数的概括过宽，已限定到相应套件。
- 混合导入的重复冲突诊断包含英文说明，而其他错误为中文。记录为中文文案一致性观察项，功能诊断有效，本轮未修复文案。

## 测试结束时保留的数据

- 测试副本 `webtest-manual-clone-20260910`：ID `9a8bdb0b-8088-46c1-a205-b47d6112ffe5`，r5、已启用，标签 `manual`／`webtest`／`batch-webtest`。未执行删除。
- 未提交导入预览：批次 `657dbe55-eb63-4d3c-9237-6a258322c80a`，任务 `ca5868f6-04a0-4295-a71a-31e4b4c7c918`，r2、等待确认；页面显示到期时间为 2026-09-17 12:31:36（Asia/Singapore）。
- Compose 服务保持运行；网页停在来源列表。以上是测试结束时快照，文档补录未查询或修改当前业务数据。
