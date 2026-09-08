# M1 节点、导入与配置校验接入

本契约实现 ADR-0009。来源绑定与刷新、链编排、订阅发布、连通性和下载测速不在本窗口；公开的后续接口仍返回结构化 404。

## 节点与导入

管理接口复用 Cookie 会话、Origin 和 CSRF。节点普通查询只返回配置状态，不返回认证内容；秘密查看要求近期重认证、匹配修订和审计。克隆接口为 `POST /api/v1/nodes/{id}/clone`，请求 `{ "name": "副本名称" }`，需要源 If-Match，产生新 ID／修订 1。搜索参数 `q` 仅查节点名称，协议过滤在受限候选分页内解密判断。

节点秘密补丁三态沿用省略保留、null 清除、字符串替换。非法完整组合拒绝提交。REALITY 的 PublicKey／ShortID 与普通认证字段一样触发安全 epoch 递增；重命名不触发认证撤销。安全动作不会改写历史修订。

可选 TLS 指纹允许补丁 null 清除，ALPN 补丁允许空数组清除；省略仍保留。REALITY 的必填指纹清除后仍被完整节点校验拒绝。创建及普通读取的字段约束不变。

普通管理请求的工作上下文限时十秒，数据库连接半断开时取消查询并返回安全的 503；导入确认保留 120 秒提交预算，SSE 连接保留 110 秒并支持续接。HTTP 写入时限略长于工作预算，以便返回错误响应。

`POST /api/v1/imports` 接受严格 JSON 文本或受限 UTF-8 文件，format 为 `auto`、`uri_list`、`base64_uri_list`。JSON 请求总大小使用现有 10 MiB 上限（含 JSON 编码开销）；文件内容与解码后数据各自限 10 MiB，最多 5,000 条、单 URI 16 KiB、最多两层列表 Base64。具体方言与有依据的默认值见 `parser-contract.md`。本地导入不接受 source_id 或 bind 操作。

创建批次和队列任务原子提交，返回 202。GET 候选每页最多 200 条，分页游标绑定作用域、批次及版本；每条候选携带行号相关诊断、脱敏 Node 和重复提示。连接内容 HMAC 不含资源 ID／修订／名称，不能作为稳定身份，也不自动合并节点。

确认以 If-Match 绑定预览，Idempotency-Key 绑定主体、路由与规范请求摘要；支持最多 5,000 条选中决策，create／update／skip 在同一事务完成。update 额外校验已有节点修订，任何错误回滚全部正式写入。幂等回放仅返回既有无秘密回执。未确认前正式节点库不变。

批次状态为 queued／parsing／ready／failed／committed／expired，expires_at 明确返回。原文和暂存候选保留七天；后台每分钟有界清理，避开有效任务租约和正在提交的批次。提交回执保留；到期候选不能重新确认。Worker 重启从数据库恢复，不依赖浏览器轮询维持任务。

## 任务与 Runner

队列使用 PostgreSQL，状态与 verdict 分开。领取、心跳、事件和结果绑定 job_id、worker_id、attempt、lease_seq；旧租约返回 LEASE_LOST。租约默认 30 秒、心跳 5 秒；配置错误不自动重试，基础设施错误最多重试一次。API Worker 只执行 import_parse；Runner 本窗口只执行 config_validate。

Runner 内部接口为独立 TLS 1.3 mTLS 监听器，按登记证书 DER 摘要、Runner ID、实际构建／架构和槽位验证。控制请求只能提交固定协议 DTO；Runner 不持有数据库凭证或主密钥，不能领取 API Worker 的任务。固定校验子进程限制网络、可读写文件、进程、内存和时间；隔离无法建立时失败关闭。

配置校验无需网络预算预留，max_bytes 和 settled_bytes 为零；已有网络任务契约仍强制 quota_reservation_id。内部调用 `validation.New(queue, cores).Submit(ctx, scopeID, batchID, target, artifact)` 提交编译器产物；batchID 可为空以执行独立校验。调用方保存目标与 Job ID 的关联，产物哈希和锁定构建进入加密载荷。没有任意原生配置的公开上传接口。

## 开发部署

默认开发 Compose 启动 API Worker 和管理界面；未配置传输的 Runner 保留空闲开发模式。需要实际校验时，先安装并验证已锁定内核，再创建专用开发 mTLS 身份并启用 overlay：

```powershell
node scripts/verify-cores.mjs
pwsh -NoProfile -File scripts/dev-runner-init.ps1
docker compose --env-file deploy/secrets/local/runner.env -f deploy/compose.dev.yaml -f deploy/compose.runner.yaml up --build -d --wait
```

初始化脚本不会下载或执行内核，不覆盖既有完整身份组，部分缺失时拒绝自动轮换。生成身份有效期 90 天，只用于开发；CA 私钥不落盘。Runner 只挂载自身证书、专用 CA 和只读内核目录，API 内部端口不发布到宿主机。若任一传输配置存在但不完整，必须报错，不能回退空闲模式。

生产证书生命周期、构建分发和双架构发布继续由后续部署任务完成；本窗口没有提升兼容目录的 verified 状态。
