# T-007：完整 HTTP 契约与通用组件

日期：2026-09-08。状态：实现及自动验收通过，`in_review`。

## 交付

- `api/openapi.yaml` 覆盖 SDD 管理接口、订阅路径与内部 Runner 接口：57 条路径、86 个操作、318 个 Schema；未来业务操作标记为 contract-only，不挂载成功占位处理器。
- 管理 Cookie、Runner mTLS、订阅路径令牌独立鉴权。OpenAPI 不虚构路径类型的 apiKey scheme；订阅使用必填路径参数和明确鉴权扩展，不能由 security 空数组推断匿名获取配置。
- `internal/apicontract` 提供严格 JSON、安全错误和字段定位、服务端 request_id、If-Match／ETag、用途专用签名游标、幂等请求元数据、三态秘密补丁、独立读 DTO、预览确认、父批次／子任务、租约 fencing 与 SSE 重放契约。
- JSON 限制 10 MiB、深度 128；数字字面量最多 128 字节、指数绝对值最多 308，在 Schema 前拒绝放大输入。等值整数和有限小数精确规范化，不通过 float64 丢失精度。API int64 计数为有范围约束的十进制字符串，内部 IR／存储整数不变。
- 现有 HTTP 服务接入请求 ID 与标准 404／500 错误；客户端不能指定日志关联 ID。健康检查与 HEAD 语义保持，节点 CRUD／认证／发布／Runner 业务路径仍不开放。
- 锁定 openapi-typescript 7.9.1，生成前端类型；生成前禁止远程引用，拒绝 unknown 联合扩宽，漂移检查在缓存目录重建并给出有界错误。生成文件固定 LF，支持 Windows／Linux 的一致检查。

## 验证与源码绑定

以下检查均通过：全部 OpenAPI 组件离线编译、基线 86 操作清单、请求与响应夹具、Go DTO／Schema 一致性、秘密保留／清除／替换、未知／重复字段、Unicode／null／数字上限、400／409／412／428 区分、int64 边界、游标篡改及 scope／筛选绑定、父批次／租约／SSE 形态、fmt／slog 脱敏。

`go test -mod=readonly ./api ./internal/apicontract -count=1`、对应 vet、API 生成与漂移、前端 typecheck／build 已通过；最终全工程 Windows／Linux race 和真实 API → PostgreSQL 集成也通过。共同日志与执行范围见 [T-004](../T-004/summary.md)。

- OpenAPI SHA-256：`beb7051172c2eb33eb2ce9a8e37428909757e7c9951245afcd2406970d2d065f`。
- 生成 TypeScript SHA-256：`9c0905b77196d7b7c15560181aec47ec82a6852c252f0b34dc471ed72db4fc92`。
- 最终 253 文件源码清单 SHA-256：`21d8693b97fc05d50f98d3d63e2ac40ee0411c63e397e4629190e0789224376c`。

构建过程中发现并修正了 Schema 未齐时的内部错误、秘密补丁过早按创建字段校验、format-only 联合导致 TypeScript unknown 扩宽，以及生成文件漂移时巨量输出；最终均复验。契约存在不代表未来业务处理器或真实 SSE／Runner 协议已经实现，完整 F1／G1 仍需后续任务。
