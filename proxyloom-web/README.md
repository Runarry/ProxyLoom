# ProxyLoom 前端

Vue 3、TypeScript、Vite、Vue Router 和 Pinia，使用 `src/api/schema.d.ts` 中的 OpenAPI 生成类型。
当前交付 T-047 和 T-048 的节点、文本与文件导入部分；来源刷新差异、覆盖管理、真实内核兼容检查和发布向导仍属于后续切片。

## 功能

- 初始化、登录、退出、通过 HttpOnly 会话 Cookie 恢复登录和密码重认证；认证错误、网络错误、字段错误和请求编号统一呈现。
- 六类协议动态表单；协议改变时清空旧认证、传输、安全和功能字段。已存秘密只显示“已设置”，编辑分为省略保留、字符串替换和 `null` 清除，必填秘密清除由 API 拒绝。
- 编辑时可将 ALPN 留空清除可选列表，也可将 TLS 客户端指纹留空清除；未改动的可选字段省略保留。REALITY 客户端指纹仍为必填项。
- 名称、协议、标签、启用状态筛选；签名游标分页；跨页最多选择 200 节点执行批量标签或启停；逐项展示成功、412 等结果。
- 节点详情、不可变修订历史、当前及历史引用、服务端克隆和删除。克隆调用专用 API，不读取或重发已有秘密。
- 节点秘密需重认证后显式查看；60 秒、页面切换、隐藏标签页或关闭对话框时清除。复制按钮为独立明确操作。
- 文本与 multipart 文件导入支持自动、URI 列表和 Base64 URI 列表；轮询持久批次状态，刷新 URL 可恢复脱敏预览。逐项呈现不支持、无效和重复诊断。
- 候选每页 200 项，跨页保留选择，最多 5000 项在一次请求中原子提交。匹配和重复项默认跳过，明确选择创建或更新；提交携带 `If-Match` 与幂等键。网络结果不确定且选择未变时，重试复用原键。
- 412 不覆盖节点草稿，必须读取最新版本、比较并确认修订后才能再次保存。所有保存/解析状态均与“兼容性未验证”分开展示。

应用不使用 localStorage、sessionStorage、IndexedDB、Service Worker 或持久化 Pinia 插件。
密码、初始化凭据、导入原文、编辑草稿和 CSRF 只保留在当前页面/会话的内存中；请求不缓存、不记录正文、不自动重放写操作。
页面时间使用 API 的 UTC 时间戳，按浏览器时区展示。修订号始终是无损十进制字符串。

## 开发与构建

使用 Node.js `24.6.0` 和 pnpm `10.33.2`：

```sh
pnpm install --frozen-lockfile
pnpm dev
pnpm typecheck
pnpm build
pnpm check:api
```

开发界面监听 `127.0.0.1:5173`，`/api`、`/healthz`、`/readyz` 代理到 `127.0.0.1:8080`。
认证要求精确 Origin；开发时 API 的 `PROXYLOOM_PUBLIC_URL` 应与用户实际访问的前端地址一致。
生产由 API 提供 `dist/` 和 SPA 回退，前端与 API 同源。`pnpm preview` 只预览静态文件，不代理 API。

## 验证

```sh
pnpm test
pnpm test:ui
pnpm test:e2e
```

`test` 检查秘密三态、协议切换的字段隔离、遮罩拒绝、草稿清理和可选字段保留语义。
`test:ui` 在独立的 `127.0.0.1:4173` 启动 Vite，使用合成 API 响应验证浏览器交互；这些结果不能替代真实 API 验收。
Windows 下测试脚本直接启动并关闭自身 Node 子进程，避免 shell 进程树；端口占用会失败。

Playwright 精确锁定 `1.58.2`。可先执行 `pnpm exec playwright install chromium`，或设置 `PROXYLOOM_E2E_BROWSER=chrome` 使用已安装的 Chrome。

真实 E2E 必须配置以下环境变量；缺失时命令失败，不会以跳过方式返回通过：

| 变量 | 含义 |
| --- | --- |
| `PROXYLOOM_E2E_BASE_URL` | 真实 API 提供最新 `dist/` 的同源 HTTP(S) 地址 |
| `PROXYLOOM_E2E_USERNAME` | 隔离验收工作空间的合成管理员用户名 |
| `PROXYLOOM_E2E_PASSWORD` | 对应合成管理员密码，经受控环境注入 |
| `PROXYLOOM_E2E_SETUP_TOKEN` | 可选；仅全新未初始化工作空间传入，首项测试验证初始化 |
| `PROXYLOOM_E2E_BROWSER` | 可选 Playwright 浏览器 channel，如 `chrome` |

真实用例覆盖认证、六协议创建、保留秘密编辑、克隆、修订/引用、批量标签/启停、并发修订冲突、重认证查看、205 个有效候选的跨页原子提交与 Base64 文件导入。
用例操作随机命名的合成节点并在结束时删除；应只运行在专用验收工作空间。
真实测试在工作进程内存中复用会话 Cookie，整套只执行两次登录和一次重认证，以遵守管理员认证限流；结束时撤销共享会话，不写入会话文件。
截图、视频、trace 默认关闭；UI 与真实测试的诊断目录分别为 `test-results/ui/`、`test-results/e2e/`，均不提交。

依赖采用精确版本，传递依赖及完整性摘要保存在 `pnpm-lock.yaml`。更新契约后由仓库生成脚本更新类型，不手工修改生成文件。
