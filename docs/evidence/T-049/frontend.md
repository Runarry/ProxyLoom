# T-049 M1 编排前端验证记录

日期：2026-09-10（Asia/Singapore）。源码基线：`80ad286a44c77a12c80384e70350951370be788e` 加当前未提交 M1 工作区改动；本记录不表示已提交或发布。

环境：Windows、Node.js `v24.6.0`、pnpm `10.33.2`、Playwright `1.58.2`、Microsoft Edge `152.0.4191.66`。UI 测试使用隔离的 `127.0.0.1:4173` Vite 进程、单 worker、无重试、无 trace/screenshot/video、合成无秘密 API 夹具。

## 已完成行为

- 新增链路、策略组、路由、规则集、DNS 的列表、详情、创建、编辑、删除入口；通过真实 `/api/v1` 接口提交，更新和删除使用 ETag / If-Match。候选资源读取覆盖分页且只允许启用资源。
- 链路明确显示“客户端 → 第一跳 → 最终出口”，可交换两个不同的具体节点；链路引用不修改原节点。策略组提供四种策略、有序节点/链路成员、明确默认成员及健康检查字段；不把结构保存表述为内核或客户端验证。
- 路由通过类型化字段维护 AND/OR 条件、顺序首匹配、显式最终动作及 Node/Chain/PolicyGroup 目标。上移/下移按钮支持键盘操作。
- DNS 显示独立 bootstrap、业务 resolver、HTTPS 引导引用、显式出站、默认 resolver 及有序域名规则。重命名同步本地引用；切换类型不提交已隐藏字段；服务端错误保留并可跳转到对应控件。
- 规则文本在浏览器规范化后仅提交 `domain_cidr_text` entries；注释/空行不进入条目，服务端 `/entries/N` 路径映射回原始文本行。预览和保存不接受原生动作规则或任意文件路径。
- 412 保留当前草稿，加载最新版本后可采用最新内容，或确认比较后以最新修订号保留草稿。网络资源采用最新内容时先对 Vue 响应对象取原始值再复制，避免 reactive clone 错误。
- 客户端预设只读展示五个系统资源，包括三个关闭控制接口的原预设和 `127.0.0.1:17812` / `127.0.0.1:17813` 控制变体。仅展示 API 返回的审核状态；明确区分内核加载、运行时行为及客户端导入，并将没有结果的验证项标为 unverified。
- 修复新增导航在手机宽度下溢出：导航自动换行，原有手机布局与键盘跳转检查通过。通用错误跳转支持数组元素或嵌套引用路径对应的最近输入字段，例如 `/dns_profile/resolvers/1/outbound/resource_id` 聚焦出站选择器。

## 实际执行

命令除 `git diff --check` 外均在 `proxyloom-web/` 运行。最终接口检查及构建在父任务更新固定 SOCKS `127.0.0.1:1080` 与控制端口约束、重新生成 API 类型之后执行。

| 命令 | 结果 |
| --- | --- |
| `pnpm check:api` | PASS，生成类型与本地 OpenAPI 再生成结果一致。 |
| `pnpm test` | PASS，21/21：网络草稿 4、规则文本/规范化 5、节点草稿 7、链路/策略组 5。 |
| `pnpm build` | PASS，包含 `vue-tsc --noEmit`；Vite 转换 110 个模块并生成生产输出。 |
| `pnpm exec tsc --noEmit --strict --skipLibCheck --target ES2022 --module ESNext --moduleResolution Bundler tests/ui/network-orchestration.spec.ts` | PASS，新网络 UI 夹具与生成类型匹配。 |
| `$env:PROXYLOOM_E2E_BROWSER='msedge'; pnpm test:ui network-orchestration.spec.ts` | PASS，最终源码 7/7，10.3 秒。 |
| `$env:PROXYLOOM_E2E_BROWSER='msedge'; pnpm test:ui` | 33/34 PASS；命令 exit 1。最后一个原有来源/节点导出用例在 `browser.newContext` 阶段遇到 Edge 关闭，尚未执行应用断言。 |
| `$env:PROXYLOOM_E2E_BROWSER='msedge'; pnpm test:ui source-management.spec.ts -g 'node export never exceeds'` | 隔离重跑 PASS，1/1，17.8 秒；测试本体 8.1 秒。未降低断言或增加自动重试。 |
| `git diff --check` | PASS，无空白错误；仅提示工作区既有 LF/CRLF 转换。 |

完整 UI 运行包含：原管理页 10 项、网络编排 7 项、链路/策略组 6 项、来源管理 11 项。新增导航最初导致手机宽度检查失败，修复后该项通过。开发中的网络 UI 夹具曾使用错误的条目属性及过严的 label 选择器；现已改为契约字段和可访问角色选择器，最终 7 项通过。

最后一项完整运行失败紧接下载导出用例，浏览器日志出现 `ITaskbarList3` 初始化错误，随后 `browser.newContext: Target page, context or browser has been closed`。隔离运行通过，支持浏览器进程生命周期问题的判断；不把完整 34 项命令记为通过。

## 可追溯范围

最终网络 UI 的七个用例覆盖：类型化动作及键盘重排、DNS 引用重命名/类型切换/服务端错误聚焦、规则文本规范化及原文行号、路由/DNS/规则集分别处理 412 并采用最新版本、五个只读预设的控制接口与筛选。链路/策略组 UI 用例覆盖创建、候选分页、默认成员、412 保留草稿再提交、列表筛选与删除修订号。

关键文件 SHA-256：

| 文件（相对 `proxyloom-web/`） | SHA-256 |
| --- | --- |
| `src/views/NetworkEditorPage.vue` | `038a8ade46b8bf973cb359acdbfbffe9128feef8bee3c8df91d81ce50b0deb89` |
| `src/views/ClientPresetsPage.vue` | `69cff30b86a574862672f5e94d79bb477b63419f292344a24b45b91405dae067` |
| `src/domain/network-draft.ts` | `6116e268a93dc3b678ce7e1f6d6db65554086714518d6d76ed59e7fb491ed0f6` |
| `tests/ui/network-orchestration.spec.ts` | `bc0629f0a354dea0cde1a2e0245ee5145e4c5321c78154f24dd365828c392d9c` |

本记录中的浏览器 UI 检查模拟 API 响应，不能替代 PostgreSQL + 真实 API 的 CRUD 联验、真实内核加载/运行测试或具体客户端导入验证。真实 API 浏览器联验由独立验收任务维护 `tests/e2e/orchestration.spec.ts` 及其证据；内核与能力状态由对应后端证据负责。本前端没有订阅发布、运行时测试结果或伪造的 verified 状态。
