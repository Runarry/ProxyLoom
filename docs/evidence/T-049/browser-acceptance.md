# T-049 编排管理界面真实浏览器验收 — 2026-09-10

## 范围与代码状态

本验收通过生产构建的 Vue 页面、真实 Go HTTP handler 和临时 PostgreSQL 数据库检查链路、策略组、规则集、路由、DNS 与只读客户端预设。浏览器只通过公开管理 API 写入和读取资源；没有用合成 API 响应替代持久化结果。

- Git 基点：`80ad286a44c77a12c80384e70350951370be788e` 加当前未提交的 M1 编排／DNS／预设集成工作树。
- 浏览器 harness SHA-256：`E2DB6B24403E377238BD97FECF8E8F13C4222ACBA48EBFD828F5C2BE4BB1A709`。
- Playwright spec SHA-256：`D409C79E7E1EB64DDF551B0E2B185387F319DC05044EC7FE38949C7A1FC1CC54`。
- 受测生产构建 `dist/index.html` SHA-256：`43BF562D28BB7ACB404956777AE2DDA9402DEA822D77523E91CA117FE035BCB0`。
- 最终运行输出 SHA-256：`864B9DE96F870527570E40458247D30C22205A110F2165750F5BBD320677833F`。
- 环境：Windows amd64、Go 1.26.0、Node 24.6.0、pnpm 10.33.2、Playwright 1.58.2、系统 Microsoft Edge、锁定 PostgreSQL 17.6 镜像。
- 数据库入口使用父验收任务拥有的 `proxyloom-acceptance-500cca68-994b-4459-998e-788c7aebcdc7` 容器。测试内部创建独立数据库、迁移与运行角色；DSN 和随机管理员凭据只通过忽略目录／进程环境传递，未写入本证据。
- handler 构造前显式调用与正式启动相同的 `EnsureBuiltinClientPresets` provisioning；客户端预设 GET 保持只读，不在请求中隐式创建数据。

## 结果

| 状态 | 检查 | 操作与结果 |
| --- | --- | --- |
| **PASS** | 生产前端构建 | `pnpm build` 通过类型检查；Vite 8.2.2 转换 110 个模块并完成生产产物。 |
| **PASS** | 真实编排浏览器总入口 | 最终 DNS 说明文字及同步契约／生成类型落定后重新执行生产构建；设置 `PROXYLOOM_ORCHESTRATION_BROWSER_TEST=true`、隔离 PG DSN file、`PROXYLOOM_E2E_BROWSER=msedge`，运行 `go test -mod=readonly -count=1 ./internal/storage -run '^TestOrchestrationBrowserAcceptance$' -timeout=8m -v`。最终留档重跑中 Playwright 4／4 在 9.8 秒通过；含服务与数据库 harness 的 Go 测试在 13.059 秒通过。原始输出保存在忽略目录 `.cache/m1-review-acceptance/orchestration-final.txt`。 |
| **PASS** | 链路创建、交换和原节点不变 | 页面从真实已启用节点候选创建 A→B，API 读回有序 hops；编辑页交换为 B→A 并保存 r2。随后逐个真实读取三个原节点，修订和 security epoch 均仍为 1。 |
| **PASS** | 策略成员、默认项与 412 保稿 | 页面创建 `round_robin` 策略，成员为一个 Node 与一个 Chain，默认项为 Chain；API 读回策略和两个成员不变。测试在编辑期间用真实并发 PATCH 推进到 r2，页面提交收到 412，保留完整本地名称草稿；加载差异并明确确认后以新 ETag 保存 r3，策略仍为 `round_robin`。 |
| **PASS** | 规则文本行号与规范化持久化 | 第二行 `192.0.2.1/24` 的非规范网段在页面显示可点击“第 2 行”；改为有效文本后，真实 API 读回小写域名后缀、规范 CIDR 和 64 位十六进制 content hash。另直接向真实 API 提交第二项非法 CIDR，得到 HTTP 422，details 路径以 `/rule_set/entries/1` 开头。 |
| **PASS** | 路由顺序、引用与编辑 | 页面创建两条有序规则，第二条上移后保存；真实 API 顺序为 `second-before-move`、`first-before-move`，首条保持 direct 动作，后条保持刚创建的 RuleSet 引用。名称编辑生成 r2，规则顺序未变化。 |
| **PASS** | DNS 显式解析器、循环诊断与编辑 | 页面在默认 local 解析器外增加显式 HTTPS resolver、固定 bootstrap 与 direct 出站，真实 API 读回 `[local, https]` 和显式 final resolver；名称编辑生成 r2。另向真实 API 提交两个 HTTPS resolver 互相 bootstrap 的循环，HTTP 422 同时定位 `/dns_profile/resolvers/0/bootstrap_resolver_id` 与 `/dns_profile/resolvers/1/bootstrap_resolver_id`，响应不回显测试 URL。 |
| **PASS** | 五个只读客户端预设 | 页面真实读取 provisioning 后的 5 个预设，只显示“系统预设 · 只读”，没有创建或编辑入口。 |
| **PASS** | 浏览器秘密边界 | 每项结束均检查 `localStorage` 与 `sessionStorage` 为空。节点夹具使用无需凭据的 HTTP Node；随机管理员密码没有出现在 Playwright/Go 输出。 |
| **PASS** | 普通测试门槛 | 未设置 opt-in flag 时 `go test ... -run '^TestOrchestrationBrowserAcceptance$' -v` 按设计 SKIP，包通过；普通单元测试不会意外启动浏览器或 PostgreSQL 验收。 |

## 诊断运行

首次运行中 Chain／DNS 的 `getByLabel(..., exact)` 在真实 select 的可访问结构上等待超时，Policy 因依赖该失败用例创建的 Chain 而找不到夹具，RuleSet 非规范 CIDR 的真实服务状态为 422 而测试误期望 400。这些均为验收代码问题：

- select 改用 role + 精确 accessible name；
- Policy 使用 `beforeAll` 经真实 API 独立创建的 Chain；
- 非规范 CIDR 按实现契约断言 422，并继续验证精确 entries 索引。

第二轮 Chain／Policy／DNS 通过，Routing 的命中动作 select 仍使用了旧定位方式；更正为 role 后完整四项重跑通过。没有为通过而放宽产品断言、跳过用例或修改应用代码。

## 限制

- 本验收证明管理面 CRUD、修订冲突、引用与诊断，不证明三内核的完整策略／路由／DNS 映射、配置加载或真实流量行为；那些结论须由 T-029／T-030 和真实内核专项提供。
- 未在真实浏览器重复全部列表筛选、分页、删除和所有字段组合；这些属于现有类型化单元、真实 PostgreSQL API 与合成 UI 回归的互补范围。
- 本记录对应上述未提交工作树摘要。父任务仍须在所有写入停止后执行最终源码清单与统一质量／PostgreSQL 验收，不能把本次结果外推到随后变化的文件。

## 本验收新增文件

- `internal/storage/orchestration_browser_test.go`
- `proxyloom-web/tests/e2e/orchestration.spec.ts`
- `docs/evidence/T-049/browser-acceptance.md`

未修改应用代码、依赖、锁文件、迁移、任务状态或能力状态。
