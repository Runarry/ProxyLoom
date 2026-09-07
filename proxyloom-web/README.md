# ProxyLoom 前端

中文工程启动页，使用 Vue 3、TypeScript、Vite、Vue Router 和 Pinia。
当前没有登录、资源管理、订阅发布或内核执行功能；不检测服务状态，也不保存浏览器持久状态。
Pinia 维护职责说明的展开状态，Vue Router 提供首页及未知路径回退。

## 本地开发

使用 Node.js `24.6.0` 和 pnpm `10.33.2`，在本目录执行：

```sh
pnpm install --frozen-lockfile
pnpm dev
```

开发服务器仅监听 `127.0.0.1:5173`；端口被占用时退出。
`/healthz` 和 `/readyz` 代理到 `http://127.0.0.1:8080`，供独立启动的 API 使用。

## 验证与构建

```sh
pnpm typecheck
pnpm build
pnpm preview
```

`build` 包含类型检查，静态产物位于 `dist/`。生产部署由 API 服务此目录及 SPA 回退；
Vite 开发代理不包含在生产产物中。`preview` 仅供本地查看静态产物，不代理 API。

依赖使用精确版本，传递依赖与完整性摘要记录在 `pnpm-lock.yaml`。
版本已通过官方 npm registry 元数据核对；保留 Vue Router 4、Pinia 3、TypeScript 5 的相容主版本。
