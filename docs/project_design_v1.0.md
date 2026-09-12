# 多内核订阅管理平台
## 完整项目设计文档

文档编号：PSB-SDD-001  
版本：v1.0 · 设计评审稿  
编制日期：2026-09-07  
适用对象：架构、Go 后端、前端、测试、安全、运维  
需求基线：《需求分析文档》PSB-SRS-001 v1.0

> 本文给出从领域模型到发布、测试和部署的完整设计基线。结构、接口与示例用于指导实现，不代表已经交付或运行过的项目代码。涉及外部内核的事实按官方资料核对；内核版本、构建摘要和客户端验证结果须在实施阶段锁定，不使用虚构的“已通过”记录。

# 1. 设计目标与关键决策

## 1.1 设计目标

用一套业务模型维护节点和编排意图，通过三个独立编译器生成目标配置，以可追溯的修订和发布快照保证一致性，以独立 Runner 验证配置与网络行为。平台必须让不兼容性可见，让安全变更及时阻断旧产物，让测试资源和流量可控。

本期采用单工作空间、可信管理员、自托管 Linux Docker 场景。前后端代码分离，Go 负责 API、任务调度和 Runner。系统不承担客户端长期代理流量，不管理远端服务端账户，也不提供任意跨协议网关。

## 1.2 架构决策记录

| 决策 | 选择 | 理由与代价 |
| --- | --- | --- |
| ADR-01 | 模块化单体 API＋独立 Runner | 减少分布式业务复杂度；将高风险进程和探测独立；后续可拆分任务服务 |
| ADR-02 | PostgreSQL 单一持久层 | 修订、引用、事务发布和任务租约统一；暂不双轨支持 SQLite |
| ADR-03 | 自定义类型化 IR，不以任一内核配置为数据库模型 | 保留连接语义与跨内核诊断；需要维护明确适配器 |
| ADR-04 | 业务资源注册表＋不可变修订＋强引用边 | 统一版本与依赖机制；类型约束同时由应用和数据库触发器验证 |
| ADR-05 | 内核独立二进制，不嵌入业务库 | 分离版本和崩溃；需要管理子进程、构建摘要与临时目录 |
| ADR-06 | 严格发布，所有启用目标一起通过后切换 | 避免同组不同目标版本漂移；单个目标不兼容会阻止整批发布 |
| ADR-07 | PostgreSQL 持久任务队列，P0 无 Redis | 利用事务和租约恢复；不承诺超大规模消息系统吞吐 |
| ADR-08 | 管理端会话 Cookie；订阅端独立令牌；Runner mTLS | 三类信任边界分离；证书和密钥需要部署管理 |
| ADR-09 | 发布物加密存储，返回前实时授权 | 缓存不能绕过撤销；数据库不可用时订阅失败关闭 |
| ADR-10 | 两跳具体节点，策略组不嵌套 | 降低图展开与运行时语义复杂度；多跳和动态链放到后续 |
| ADR-11 | P0 规则内联，客户端完整配置优先 | 避免客户端外部依赖及规则资源授权混乱；大规则集后续增加格式适配 |
| ADR-12 | 只对实际测试过的组合标记 verified | 不依赖粗粒度“支持协议”勾选；需持续维护样例和 CI |

## 1.3 设计不变量

节点身份与展示名称分离；节点原始配置不包含链式关系；任何出站依赖必须完整展开；普通配置转换不得改变协议语义；配置失败和链失败均不隐式直连；订阅请求不执行编译或测速；每份发布物能追溯全部依赖修订、目标构建和编译器版本。

密钥轮换、节点停用和权限撤销优先于历史版本可用性。旧发布继续服务的前提是其仍通过当前安全检查，而不是只要曾经成功编译就永久可下载。

# 2. 总体架构与模块边界

## 2.1 逻辑架构

```text
浏览器管理端（Vue 3 + TypeScript）
        | HTTPS / 管理会话
        v
Go API
  |-- Identity：管理员、会话、敏感操作
  |-- Catalog：节点、来源、链、策略、路由、DNS
  |-- Import：受限抓取、解析、去重、人工覆盖
  |-- Compiler：快照冻结、能力检查、三目标生成
  |-- Publication：原子发布、令牌、订阅读取
  |-- Jobs：持久队列、租约、预算、事件
  |-- Operations：审计、保留策略、指标
  |                 |
  v                 | mTLS，独立内部监听器
PostgreSQL          v
                 Go Runner
                   |-- 任务验证与资源调度
                   |-- Xray 进程
                   |-- sing-box 进程
                   |-- Mihomo 进程
                   |-- 本地 SOCKS 探测客户端
                   `-- 受控测试目标

订阅客户端 --HTTPS + 独立令牌--> API /s/... --> 有效发布快照
```

普通业务请求、公开订阅请求和 Runner 回传共用业务服务层，但使用不同路由、中间件和监听范围。API 不直接运行内核；Runner 不访问 PostgreSQL、不持有全局解密密钥。

## 2.2 技术栈与部署单元

| 层级 | 拟定技术 | 说明 |
| --- | --- | --- |
| 管理界面 | Vue 3、TypeScript、Vite、Vue Router、Pinia | 动态字段与状态机驱动；UI 组件库可采用 Naive UI，冻结依赖版本 |
| API | Go、Gin、标准 context/slog | 模块化服务层；数据库和外部调用有超时 |
| 数据访问 | pgx、sqlc、版本化 SQL migration | 领域对象不与 ORM 实体强耦合 |
| 数据校验 | JSON Schema＋Go 类型及语义校验 | Schema 校验结构，Go 校验引用、策略和组合限制 |
| 任务与事件 | PostgreSQL 队列表、追加事件、SSE | 事件只是通知，任务表为最终状态来源 |
| 内核执行 | Go os/exec、进程组管理、独立工作目录 | 不使用 shell 拼接参数，不接收任意命令 |
| 测试 | Go 测试、模糊测试、集成网络夹具、浏览器 E2E | 性能测试与公网节点质量分开 |
| 部署 | Docker Compose、固定镜像摘要 | API 与 Runner 独立镜像；前端静态文件可打包到 API 镜像 |

以上是项目选型，不声称这些组件的最新版本已经适配。前后端代码和构建流程分离，不要求静态前端必须有独立常驻进程。

## 2.3 代码组织

```text
web/                         前端源码、组件、页面、生成的 API 类型
cmd/api/                     API 与迁移子命令
cmd/runner/                  Runner 主程序
internal/identity/           认证、会话、敏感操作
internal/catalog/            资源、修订、引用及领域服务
internal/ir/                 类型化中间模型、Schema、迁移
internal/importer/           分享链接解析、来源匹配、覆盖
internal/safefetch/          受限 HTTP 抓取、DNS/IP 校验
internal/capability/         构建能力与客户端能力检查
internal/compiler/           图展开、公共路由与确定性生成
internal/adapters/xray/       Xray 配置、命令和诊断适配
internal/adapters/singbox/    sing-box 适配
internal/adapters/mihomo/     Mihomo 适配
internal/publication/        快照、目标产物、发布与令牌
internal/jobs/               队列、租约、幂等与预算
internal/runner/             进程、端口、探测和回收
internal/storage/            sqlc 生成代码与事务封装
internal/observability/      日志、审计、指标和事件
api/openapi.yaml             对外 API 契约
schemas/                     IR 及字段 Schema
migrations/                  数据库迁移
fixtures/                    链接、配置、网络与负例夹具
compat/                      版本锁、能力清单、验证记录
deploy/                      Dockerfile、Compose、运维脚本
```

依赖方向为 HTTP/任务入口→应用服务→领域模型→仓储接口。适配器依赖 IR，不反向依赖 HTTP 路由；数据库包不定义业务兼容规则。编译器不得主动读取网络或当前时间，应由调用者传入冻结输入。

# 3. 统一领域模型与契约

## 3.1 资源模型

可版本化资源统一使用 resource_id、kind、revision、schema_version、name、tags、enabled、security_epoch 和 payload。资源 ID 使用应用生成的 UUID；名称允许重复，由 UI 辅助区分。对象引用必须使用 ID，不能依赖名称。

kind 包括 node、chain、policy_group、routing_profile、dns_profile、rule_set、client_preset、subscription_profile、source。所有载荷由相应 JSON Schema 及 Go 类型校验，不将 payload 当作任意自由 JSON。

scope_id 在 P0 只有一个固定工作空间，所有读写显式带 scope 条件。此字段用于防止未来迁移困难，不代表已实现多租户授权或安全沙箱。

## 3.2 Node 模型

| 字段 | 类型／规则 | 语义 |
| --- | --- | --- |
| protocol | 枚举 | shadowsocks、vmess、vless、trojan、socks5、http 等 |
| endpoint.host | 域名或 IP 字符串 | 域名标准化、IPv6 单独解析；不含 URI 端口和路径 |
| endpoint.port | 1～65535 整数 | 原服务端口 |
| auth | 协议判别联合类型 | method/password、uuid、username/password 等，属于秘密 |
| transport | 类型化对象 | kind=native_tcp/websocket/...，含必要 path、host、service_name |
| security | 类型化对象 | mode=none/tls/reality；SNI、ALPN、证书校验及协议专属参数 |
| features | 明确选项 | UDP、复用、协议变体；未设置与显式 false 不混淆 |
| extensions | 内核命名空间对象 | 仅白名单字段；不可覆盖身份、依赖和执行安全字段 |
| origin | 来源信息 | 来源资源、来源条目、匹配方法；不作为业务引用键 |

Node 不保存 detour、dialerProxy 或 dialer-proxy；这些依赖由 Chain 编译阶段生成。协议认证参数与传输、安全字段分离，例如 UUID 不是 TLS 参数，WebSocket Host 不一定等于服务器地址或 SNI。

```json
{
  "schema_version": 1,
  "protocol": "trojan",
  "endpoint": {"host": "proxy.example.invalid", "port": 443},
  "auth": {"kind": "password", "password": "EXAMPLE_ONLY"},
  "transport": {"kind": "native_tcp"},
  "security": {
    "mode": "tls",
    "server_name": "proxy.example.invalid",
    "verify_certificate": true
  },
  "features": {"udp": false},
  "extensions": {}
}
```

示例域名和密码仅作结构展示，不能连接。GET 默认返回 has_password 等状态，不返回该 auth 明文。更新时秘密字段缺省表示保留；显式 null 仅可清除协议允许为空的字段；空串不等于保留，遮罩字符串永远不能当作真实凭证保存。

## 3.3 Chain 与引用

```json
{
  "schema_version": 1,
  "hops": [
    {"node_id": "11111111-1111-4111-8111-111111111111"},
    {"node_id": "22222222-2222-4222-8222-222222222222"}
  ],
  "failure_policy": "fail_closed"
}
```

hops 以客户端观察的顺序排列，第一项为第一跳，最后一项为最终出口。P0 长度必须等于 2、ID 不重复、kind 必须为 node。业务编辑中的引用默认跟随对象头修订；编译时全部解析成具体 revision。P0 不开放手工固定到旧秘密修订的功能。

引用统一表示为 TargetRef：resource_ref 携带资源 ID；builtin 只允许 direct、reject。不同字段有不同允许集合：Chain 只允许 node；PolicyGroup 只允许 node/chain；路由可以引用 node/chain/policy_group 或 builtin。

## 3.4 PolicyGroup、路由和 DNS

| 对象 | 必填结构 | 约束 |
| --- | --- | --- |
| PolicyGroup | strategy、members、default_member、health_check、on_unavailable | fixed/manual_select/latency_best/round_robin；P0 不嵌套；无隐式 direct |
| RoutingProfile | rules、final、domain_resolution_mode | 按数组顺序，规则间首个匹配生效；final 必填 |
| Rule | match、action、enabled、comment | 不同字段 AND，同字段数组 OR；P0 不开放通用 NOT/任意表达式 |
| DNSProfile | bootstrap、resolvers、rules、final_resolver | 引导解析和业务解析分离；解析器和出站引用共同做循环检查 |
| RuleSet | format=domain_cidr_text、entries、content_hash | P0 只含标准域名/CIDR；展开后固定修订 |

域名后缀表示目标域本身及其子域，不表示普通字符串后缀。域名标准化后做 IDNA/大小写处理。IP 规则对域名目标是否触发解析由 domain_resolution_mode 控制；不能让某一内核的隐式默认改变其他目标行为。无法证明语义一致的组合返回兼容错误。

策略组的 health_check 描述客户端运行时测试参数，与平台后台 TestJob 完全分开。后台测速不会修改已经发给客户端的实时选择状态。fixed 是发布时选定出口；manual_select 是客户端运行时选择，二者不可混淆。

## 3.5 订阅方案与目标预设

```json
{
  "schema_version": 1,
  "members": {
    "include_ids": [],
    "exclude_ids": [],
    "selector": {"all_tags": ["home"], "any_tags": [], "none_tags": []}
  },
  "routing_profile_id": "33333333-3333-4333-8333-333333333333",
  "dns_profile_id": "44444444-4444-4444-8444-444444444444",
  "targets": [
    {
      "key": "mihomo-default",
      "core_build_id": "55555555-5555-4555-8555-555555555555",
      "client_preset_id": "66666666-6666-4666-8666-666666666666",
      "format": "mihomo_yaml",
      "policy_overrides": []
    }
  ],
  "publish_policy": "strict_all_targets"
}
```

target.key 是稳定路由键，不以 User-Agent 猜测目标。允许为同组建立 xray-default、singbox-default、mihomo-default 等目标，以及不同客户端预设。目标覆盖只允许特定策略与客户端参数，不允许覆盖节点身份、秘密、安全校验或绕过引用检查。

成员计算为：基础成员=(include_ids∪标签筛选结果)−exclude_ids；实际配置成员=基础成员及路由、DNS、策略、链的依赖闭包。若 exclude_ids 命中必要依赖，则报错。自动依赖必须在发布预览中显式列出并确认，不以“隐藏节点”掩盖凭证分发。

# 4. 数据库设计与一致性

## 4.1 数据模型选择

采用“关系型元数据＋类型化、加密的不可变载荷”。频繁检索的名称、kind、标签、状态和时间单独索引；包含地址、认证、来源 URL、原始输入及完整配置的载荷存为加密封装，不写入普通 JSONB 搜索列。

本设计不为每个内核维护一套 node 表，也不同时维护互相竞争的节点事实源。resources 与 resource_revisions 是业务配置唯一事实源；source_items 保存来源基线；节点有效修订由来源基线叠加覆盖后物化生成。

## 4.2 业务与配置表

除说明外，ID 为 UUID，时间为 timestamptz，版本为 bigint，所有表都有必要的 created_at。表中的 envelope 指带版本、key_id、随机 nonce、包裹数据密钥和认证密文的加密封装。

| 表 | 主要字段 | 主键、约束与索引 |
| --- | --- | --- |
| scopes | id、name、catalog_revision、auth_epoch | PK id；P0 单行；配置变更递增 catalog_revision |
| users | id、scope_id、login、password_hash、role、disabled、auth_version | UNIQUE(scope_id,login)；role P0 仅 admin |
| sessions | id_hash、user_id、auth_version、expires_at、last_seen_at、reauth_at | PK id_hash；失效时间索引；Cookie 原值不落库 |
| resources | id、scope_id、kind、name、head_revision、enabled、deleted_at、security_epoch | PK id；UNIQUE(scope_id,id)；kind/name/状态索引 |
| resource_revisions | resource_id、revision、schema_version、envelope、content_hmac、created_by | PK(resource_id,revision)；只追加；资源 FK |
| resource_refs | source_id、source_revision、path、target_id、target_revision、expected_kind | PK(source_id,source_revision,path)；源修订/目标资源 FK；反向索引 target_id |
| resource_tags | resource_id、tag | PK(resource_id,tag)；tag 索引；标签改动计入 catalog_revision |
| source_snapshots | id、source_id、source_revision、envelope、content_hmac、http_meta、state | 来源 FK；(source_id,created_at) 索引；HTTP 元数据不含秘密 |
| source_items | id、source_id、external_key、base_envelope、base_revision、last_seen_at、state | 外部 key 存在时 UNIQUE(source_id,external_key)；禁止凭名称自动合并 |
| node_bindings | node_id、source_item_id、override_envelope、binding_revision、match_method | node_id PK/FK；source_item_id UNIQUE；一个来源条目对应一个原始节点 |
| import_batches | id、scope_id、source_id、envelope、state、expires_at、request_key | 状态/过期索引；解析预览有时限 |
| import_items | batch_id、ordinal、diagnostics、candidate_envelope、decision | PK(batch_id,ordinal)；诊断脱敏 |
| system_settings | scope_id、key、revision、value_envelope、updated_by | PK(scope_id,key)；仅白名单设置；敏感值不回显 |
| test_targets | id、scope_id、name、revision、definition_envelope、enabled | 定义 URL、预期响应、允许模式和上限；任务冻结目标修订 |
| core_builds | id、family、version、arch、os、features、sha256、adapter_version、status | UNIQUE(family,version,arch,sha256)；版本原字符串保留 |
| compatibility_evidence | id、suite_version、build_manifest、result_summary、report_envelope、verified_at | 测试证据不可变；保留运行环境与失败项 |
| capability_records | id、core_build_id、capability_key、constraint_json、state、evidence_id | 目标构建 FK；规则版本和证据关联；未验证不标 verified |

head_revision 通过延迟外键指向 resource_revisions；在同一事务中先创建资源、写入首个修订，再设置头指针。resource_refs 的目标版本为空表示编辑态跟随头；非空时必须存在相应修订。应用与约束触发器验证 scope 一致及 expected_kind，禁止凭多态 UUID 绕过类型检查。

## 4.3 编译、发布和任务表

| 表 | 主要字段 | 约束与用途 |
| --- | --- | --- |
| compile_batches | id、profile_id、profile_revision、catalog_revision、frozen_envelope、input_hmac、state、diagnostics | 冻结输入及全部解析后的目标；单批可有多个目标 |
| compile_outputs | batch_id、target_key、core_build_id、envelope、content_hmac、validation_state、validation_job_id | PK(batch_id,target_key)；存最终字节，不在 GET 时重新序列化 |
| publications | id、profile_id、generation、batch_id、state、created_by | UNIQUE(profile_id,generation)；仅合格编译可关联 |
| publication_heads | profile_id、publication_id | profile_id PK；复合 FK 保证发布与方案一致 |
| publication_outputs | publication_id、target_key、compile_batch_id | PK(publication_id,target_key)；指向对应 compile_outputs |
| publication_dependencies | publication_id、resource_id、revision、security_epoch | PK(publication_id,resource_id)；安全检查与回溯索引 |
| subscription_tokens | id、scope_id、profile_id、public_id、secret_digest、allowed_targets、expires_at、revoked_at、auth_epoch | public_id UNIQUE；原令牌不保存；target 集合服务端校验 |
| runners | id、certificate_fingerprint、capabilities、status、last_seen_at | 无数据库账号；证书身份与可领取类型绑定 |
| jobs | id、scope_id、kind、core_build_id、state、payload_envelope、priority、available_at、attempt、max_attempts、lease_seq、lease_until、runner_id、cancel_requested_at | 待执行部分索引；任务输入不可变；租约字段 CAS 更新 |
| job_events | job_id、seq、event_type、payload_redacted、created_at | PK(job_id,seq)；SSE 重放依据 |
| test_results | job_id、attempt、verdict、subject_id、subject_revision、core_build_id、runner_id、metrics_json、error_code、result_hash | UNIQUE(job_id,attempt)；结果不可变；按对象/时间索引；被测目标修订一并冻结在任务中 |
| quota_buckets | scope_id、day_utc、limit_bytes、reserved_bytes、used_bytes | PK(scope_id,day_utc)；数值非负；锁行预留预算 |
| quota_reservations | job_id、attempt、bucket_day、reserved_bytes、settled_bytes、state | PK(job_id,attempt)；任务结束至多结算一次 |
| idempotency_keys | principal_id、route_key、key、request_hmac、response_ref、expires_at | UNIQUE(principal_id,route_key,key)；同键不同请求返回冲突 |
| audit_events | id、actor_type、actor_id、action、resource_id、result、metadata_redacted | 追加写；不存凭证；按时间/对象检索 |

较大的源文件、冻结输入和产物 P0 可直接以 bytea 密文保存；达到容量目标后将 envelope 的密文替换为内容寻址对象存储引用，但授权、摘要和修订仍在数据库中。公开对象存储 URL 不得成为绕过订阅授权的下载通道。

## 4.4 事务、版本与删除

所有领域变更按固定顺序锁定 scope 行→相关资源行→修订/引用，提交时递增 catalog_revision。API 使用 If-Match 对 head_revision 做乐观并发检查，避免丢失更新。名称、标签、来源状态和覆写改变筛选结果，因此也计入目录版本。

安全变更包括节点停用、软删除、认证内容改变和管理员执行的强制撤销，必须递增该资源 security_epoch。普通名称和描述变更只增加修订，不阻断旧快照。来源消失变为 stale 默认不进入新发布，但已发布物是否继续服务按来源策略决定；凭证变化始终阻断旧版本。

删除默认设置 deleted_at 并改变安全 epoch；被引用的修订禁止级联清除。垃圾回收从活动发布、保留发布、任务、导入预览和备份所需清单做可达性分析，只有不可达且超过保留期的数据才可删除。

## 4.5 索引、容量与加密约束

索引以 scope＋kind＋状态和反向依赖为主，避免在密文中做模糊搜索。首期名称搜索可用前缀或受限子串，标签精确匹配；不为小规模管理后台提前引入独立搜索服务。

容量估算采用“节点数×保留修订数×单修订平均大小＋发布物＋测试历史”，具体平均字节数从实际样本测量。配置明文摘要仅在管理员与内部流程中使用；可对含秘密内容使用专用 HMAC，避免公开可离线猜测的简单凭证指纹。

# 5. 输入、来源刷新与标准化

## 5.1 解析流水线

```text
接收文本/文件/受限来源响应
  -> 长度、编码、压缩和条目限额
  -> 格式识别或显式格式选择
  -> 协议方言解析
  -> 标准化为候选 Node
  -> Schema 与语义校验
  -> 来源身份匹配与内容去重
  -> 生成脱敏差异、冲突及建议
  -> 用户确认或受控自动提交
  -> 新修订＋引用检查＋目录版本递增
```

格式识别首先处理显式 URI 和严格 JSON/YAML 结构，再尝试受限 Base64 列表；不得反复 Base64 解码直到“看起来像配置”。单 URI 保留原始方言信息，并分别处理 userinfo、query、fragment 的转义；不要对整条字符串反复 URLDecode。IPv6 使用规范的主机/端口解析，中文名称不参与去重身份。

Shadowsocks 解析按 SIP002 处理 method/password、Base64url、标签和插件标识；插件字段可以被记录但只有适配清单允许时执行对应内置能力，绝不根据分享链接自动安装或运行插件。[R15]

VMess/VLESS/Trojan 等分享链接方言以项目夹具为准。遇到相同 query key 多次出现、未知关键字段、无法区分的语义时，要求明确方言或产生诊断，不猜测密钥或传输设置。

## 5.2 来源匹配与人工覆盖

匹配顺序为：可信来源内稳定 external_key→已有人工确认绑定→完全一致内容指纹→低置信度候选建议。名称、服务器地址或协议相同都不足以自动确认身份。外部 key 仅在同一来源内唯一。

base_envelope 保存上游节点，override_envelope 保存字段白名单补丁。有效节点由 typed merge 计算，不对任意 JSON 做不受控深合并。数组一般作为整体替换，secret 字段的删除/保留规则与编辑 API 一致。覆盖来源数据的密码或地址应在 UI 单独标识。

刷新时先抓取和解析，不长时间占用数据库事务；最终提交使用 source_revision 与 binding_revision 进行 CAS。冲突则重新计算或交由人工确认，不覆盖用户正在编辑的补丁。相同来源最多一个有效刷新租约。

## 5.3 远程抓取与规则更新

SafeFetcher 使用独立 Transport、明确 DNS 解析及连接 IP 校验，不使用来自环境的 HTTP_PROXY。每次重定向重新执行协议、端口、主机和 IP 检查。检查 IPv4、IPv6、IPv4-mapped IPv6、回环、私网、链路本地、多播、未指定地址及云元数据地址。不能只用 IsGlobalUnicast 代表公网安全。[R16]

抓取绑定已验证 IP，但 HTTPS 保留原始 Host 与证书服务器名，避免验证后再次解析产生重绑定。限制连接、响应头、压缩后和解压后的体积；默认不允许跨域重定向转发认证头。响应需通过受限解析后才可成为来源快照。

P0 规则集内联，不让内核在校验或测试阶段自动下载 geodata、provider 或外部 UI。P1 规则更新由平台受控抓取并固定摘要，再生成客户端需要的内核格式与受授权附件。

# 6. 配置编译器与能力系统

## 6.1 编译阶段

| 阶段 | 输入与输出 | 失败语义 |
| --- | --- | --- |
| 1 冻结 | 在一致性事务中读取方案、选择器结果、依赖修订、安全 epoch、构建及预设 | 资源不可用或目录状态不一致则拒绝 |
| 2 展开 | 引用闭包、策略成员、链实例、路由/DNS 依赖 | 缺失、排除冲突、跨 scope、循环或超限则失败 |
| 3 检查 | 能力清单＋参数组合＋客户端要求 | unsupported/unverified 阻止发布；条件未满足也失败 |
| 4 规范化 | 确定标签、名称、顺序、路由动作和默认出口 | 不允许被默认值改变语义 |
| 5 目标生成 | 三个独立 adapter 将 IR 转为目标 AST | 不支持字段必须返回诊断 |
| 6 安全检查 | 校验生成后的监听、文件、下载、脚本和控制 API | 不能借模板或扩展突破执行边界 |
| 7 序列化 | 输出固定顺序和编码的最终字节及摘要 | 不加入随机 ID、运行时间和不稳定 map 顺序 |
| 8 原生校验 | Runner 对最终字节执行目标内核检查 | 退出失败阻止该批次发布 |
| 9 发布 | 验证输入仍有效，创建发布并切换头指针 | 并发修改导致 obsolete，而非偷偷再编译 |

冻结使用 REPEATABLE READ 的短事务，解析全部浮动引用并保存完整冻结载荷后立即释放。内核校验和联网测试不持有数据库事务。冻结输入记录 catalog_revision；发布时锁定同一 scope 行重新比较。P0 使用目录级保守检查，任何目录变更都可使批次过期，代价是无关编辑也可能需要重编译；后续可优化为依赖版本向量＋选择器结果摘要。

## 6.2 能力清单

能力键至少包括 core.family、core.build_id、feature_set、protocol、protocol_variant、transport、security_mode、udp_requirement、chain_position、route_feature、dns_feature、platform 和 client_preset。版本比较由各家适配器处理，不对所有版本字符串盲目按同一种 SemVer 排序。

capability_records 保存约束谓词和证据，不保存一个永久不变的“此内核支持此协议=true”。返回诊断例如：

```json
{
  "code": "CAPABILITY_UNSUPPORTED",
  "severity": "error",
  "resource_id": "11111111-1111-4111-8111-111111111111",
  "field_path": "/strategy",
  "target_key": "singbox-default",
  "message": "该目标未适配 round_robin，不能替换为 urltest",
  "suggested_action": "为此目标显式选定另一策略，或取消该输出目标"
}
```

现有文档可能包含开发分支的能力，构建特性也可能裁剪功能；因此 official_documented 只作为候选依据，不直接等于 verified。[R09][R10]

## 6.3 图展开与唯一标签

为每次编译构建有向图：业务引用、链跳引用和 DNS 引导依赖统一检查；使用 DFS 三色标记或拓扑排序找出环，诊断返回完整循环路径。P0 图规模按展开上限控制，避免嵌套对象或标签筛选引发资源膨胀。

生成标签不直接用用户名称。独立节点标签采用 n_<稳定短哈希>；链实例采用 c_<chain哈希>_h1、c_<chain哈希>_h2；策略成员克隆使用 g_<group哈希>_m<固定序号>_。发生短哈希碰撞时确定性增加长度或失败，不能随机生成新标签破坏可重复性。

Xray balancer 的 selector 是前缀匹配，不是精确 ID 列表。为每组生成独立、无前缀歧义的成员命名空间，只允许该组的最终出口实例匹配，不能把中间跳、独立节点或其他组意外纳入。[R03]

## 6.4 扩展合并与安全字段

生成顺序为：受信任客户端预设→结构化 IR→类型化目标覆盖→白名单扩展→系统生成的标签/依赖/安全约束→最终完整校验。系统保留字段不可由用户覆盖；冲突默认报错，而非使用“最后写入者胜出”。

禁止任意路径读写、可执行脚本、动态插件、外部 UI 下载、任意控制接口监听、主机路由管理、TUN 和自动更新作为通用原生扩展。需要这些功能的未来客户端预设必须另行定义和验证，且仍不能直接用于普通 Runner 在线测试。

## 6.5 测试与发布配置的区别

完整配置校验使用真正待发布的字节，验证其目标平台允许的结构；最小探测配置只保留被测节点/链、必要解析和本地入口，用于真实连通性。二者结果分开记录，不能以最小配置通过证明整个订阅配置正确。

当某客户端预设需要 Runner 所在平台不支持的功能时，必须使用匹配平台的验证执行器，或保持“未验证”；不得去掉平台字段后声称原完整配置通过。普通 P0 预设限制在无需 TUN/管理员权限的可加载范围。

# 7. 三内核适配与链式代理实现

## 7.1 目标适配矩阵

下表定义本项目的映射意图，不是跨版本功能担保。开启功能必须有对应构建与测试记录。

| 统一能力 | Xray 适配 | sing-box 适配 | Mihomo 适配 |
| --- | --- | --- | --- |
| 两跳链 A→B | B.streamSettings.sockopt.dialerProxy=A | B.detour=A | B.dialer-proxy=A |
| 固定出口 fixed | 路由直指目标出站 | 路由指向目标出站 | 规则指向指定代理 |
| 运行时手动选择 | P0 不做通用原生等价承诺 | selector＋适配的客户端控制界面 | select |
| 低延迟选优 | leastPing＋匹配的 observatory | urltest | url-test |
| 轮询分配 | roundRobin | 标准适配不支持，不用 urltest 替代 | load-balance/round-robin |
| 一致性哈希/粘性 | 不做通用等价承诺 | 不做通用等价承诺 | 按官方相应策略，P1 |
| 直连与拒绝 | freedom／blackhole，并显式路由 | 按构建使用 direct 及 route reject 动作 | DIRECT／REJECT 或对应受控出站 |
| DNS、规则集 | 独立 Xray AST | 按版本生成 DNS/route AST | 独立 YAML AST及规则行为 |

Xray 的 proxySettings 与 dialerProxy 存在冲突，默认 proxySettings 方式还可能忽略当前出站的 streamSettings，本项目统一优先采用 dialerProxy；Mihomo 旧 relay 已废弃，采用 dialer-proxy。[R01][R02][R13]

sing-box selector 的客户端控制能力需结合相应 API／客户端验证，不能只生成 selector 就宣称所有客户端均可操作。[R26] URLTest 与负载分配是不同机制。[R07][R12]

## 7.2 两跳编译算法

对于用户选择的 A→B，复制 A 为当前链的 h1 实例，复制 B 为 h2 实例；h2 通过 h1 建立连接。所有要求使用该链的路由和策略成员指向 h2，不是 h1。独立 A、B 仍使用各自不带链依赖的配置。

```text
输入：Chain(id=C, hops=[A,B])
校验：A、B 不同，存在且可用；目标支持；承载能力相容
生成：c_C_h1 = clone(A)
      c_C_h2 = clone(B)
      c_C_h2.dialer = c_C_h1
链的逻辑出口 = c_C_h2
路由/策略引用 Chain(C) -> c_C_h2
```

下列代码只展示连接依赖字段，故意省略协议、凭证及其他必填内容，不能独立作为可执行配置。正式编译器需输出完整目标 AST。

```json
{
  "tag": "c_demo_h2",
  "streamSettings": {"sockopt": {"dialerProxy": "c_demo_h1"}}
}
```

上述为 Xray 末跳字段；sing-box 的末跳片段为：

```json
{"tag": "c_demo_h2", "detour": "c_demo_h1"}
```

Mihomo 的末跳片段为：

```yaml
name: c_demo_h2
dialer-proxy: c_demo_h1
```

sing-box detour 启用时，其他拨号字段会被忽略。编译器需识别当前节点希望设置的 bind_interface、解析或其他 dial 参数与 detour 的冲突，不能无提示丢弃。[R05]

## 7.3 UDP、解析和传输承载

不能只检查两个协议是否存在。若 B 的服务端连接使用 UDP/QUIC，A 必须提供经测试可承载的 UDP 能力，且目标内核链机制支持该组合。A 具备 TCP CONNECT 不表示它能携带任意 UDP；未知组合保持 unverified。

区分 B 的服务器域名解析与业务目标解析：前者可能由 A 所在远端解析，也可能由客户端预解析，取决于具体协议与适配；必须保留验证过的语义，不统一写死成“全部本地 DNS”。在线测试为防重绑定而固定地址时，只能对不会改变协议语义的字段做替换，并记录测试连接的实际 IP。

链路验证的受控夹具需让 B 的入口仅接受 A 的测试网络来源，并在 A、B 和目标分别观察连接；这样可以验证第一跳方向。只检查目标看到的 B 出口 IP 不能证明流量一定先经过 A。

## 7.4 策略组、观测和全故障行为

Xray 低延迟策略必须生成覆盖全部成员出口的观测配置，不能漏观测链最终实例。其默认出站回落行为可能影响全故障情况，编译器必须为支持的策略显式配置拒绝回落，并把全局兜底设置为拒绝，显式业务直连规则另行添加。[R03]

sing-box/Mihomo 策略组不加入隐含 DIRECT；全部成员失效时允许连接失败或保持失败的已选代理，但不得转成直连。on_unavailable=fail_closed 表示不可越过授权出口，并不声称所有内核用同一种拒绝报文实现。

运行时选优的测试 URL、周期、超时和切换容忍度必须显式生成；不要依赖三个内核不同的默认值。负载均衡以新连接／流为单位，不承诺单条 TCP 下载带宽叠加。持续已有连接是否中断属于独立选项，需要针对客户端验证。

# 8. 发布快照、订阅令牌与并发语义

## 8.1 发布过程

```text
管理员提交 compile 请求
  -> 幂等检查并创建批次
  -> 冻结目录修订与完整依赖
  -> 编译全部启用目标
  -> 为各目标提交内核检查任务
  -> 汇总结果为 ready 或 failed
管理员提交 publish(batch_id)
  -> 锁定 scope 与方案发布头
  -> 校验目录版本、依赖安全 epoch、构建状态
  -> 创建 publication + 输出引用 + 依赖清单
  -> 单事务更新 publication_heads
  -> 提交后追加通知，客户端看到完整新版本
```

默认内核配置检查是强制发布门槛；公网连通性和测速是可选门槛。否则节点短暂网络抖动可能使所有配置发布不可用。可选连通性门槛必须指定样本有效期、被测对象修订和通过策略，不能借用过期节点结果。

每个目标的 compile_output 绑定实际内核构建、适配器、客户端预设、规则修订和序列化格式。一次发布不能混用旧批次的一部分目标，除非用户明确创建新的独立订阅方案。

## 8.2 发布安全检查与回滚

发布时验证：batch.state=ready；冻结的目录版本仍匹配；所有资源存在且 enabled；security_epoch 未变化；构建未被禁用；目标权限与输入一致。失败返回 409 COMPILE_OBSOLETE 或 422 PUBLICATION_BLOCKED，不自动用最新数据重做并覆盖用户确认。

回滚是“以历史产物创建新的发布记录”，不是篡改旧记录或无条件移动指针。需要重新校验历史依赖的当前安全 epoch、构建状态与当前方案目标授权；不匹配时拒绝。历史节点普通改名可不影响回滚，认证轮换则必然阻止旧凭证复活。

节点或构建紧急禁用后，无需等待重新编译即可使相关旧发布不可获取。后台异步标记 blocked 供界面展示，但实际下载安全检查同步完成，不依赖异步事件是否及时到达。

## 8.3 令牌设计

令牌形式为 sub_<public_id>.<random_secret>。random_secret 使用密码学安全随机源生成 32 字节后 Base64url 编码；public_id 仅用于查找，不承载权限。数据库保存 HMAC(token_pepper, public_id＋secret) 并以常量时间比较，原始令牌只在签发响应中返回一次。

allowed_targets 为该方案目标 key 集合，签发及使用时双重验证。令牌绑定 scope.auth_epoch，备份恢复或全局紧急轮换时可统一失效。令牌不是管理员 session，不接受其访问 /api/v1 或 Runner 内部接口。

令牌签发的幂等重放只返回已签发元数据，不再返回原始秘密；若首次响应丢失，管理员应撤销后用新幂等键重新签发，不能为方便重放把明文令牌持久化。

P0 不支持按 HTTP 客户端 IP 强绑定令牌，也不尝试用 User-Agent 做可靠身份识别。最近访问时间可异步合并以减少写热点，但权限判定必须同步读取已提交状态。

## 8.4 订阅读取与缓存

```text
GET /s/{token}/{target_key}
  -> 解析 token 并做请求限流
  -> 主数据库验证摘要、到期、撤销、scope epoch
  -> 校验 target 授权与当前方案状态
  -> 找到 active publication
  -> 检查其资源/构建当前安全状态
  -> 读取缓存或数据库密文并解密
  -> 返回该版本固定字节
```

P0 返回 Cache-Control: private, no-store，不开放共享 CDN 缓存，也不依赖 304 作为优化。内部可缓存按 publication_id＋target_key＋content_hmac 寻址的密文或短生命周期明文，但任何缓存命中前均完成当前数据库权限检查。不得仅以“缓存 TTL 很短”代替撤销检查。

无效、过期、撤销、目标不授权的令牌统一返回 404 和通用内容。已获授权但无有效发布返回 503 SUBSCRIPTION_NOT_READY 或 SUBSCRIPTION_BLOCKED；不返回 HTTP 200 空配置、默认直连或错误页包装的配置。

返回格式为 application/json、application/yaml 或 text/plain；设置正确字符集与 X-Content-Type-Options: nosniff。公开端点日志使用模板路径 /s/:token/:target，避免记录完整 URI。P0 不填造 Subscription-Userinfo 流量信息。

## 8.5 一致性边界

所有订阅授权读使用主数据库，不使用可能延迟的只读副本。撤销成功的定义是事务已提交；在其后开始的新请求必须被拒绝。已经完成授权且正在传输的响应无法保证撤回，这一边界必须在产品说明中明示。

数据库不可用时返回 503，不能从“上次允许”的缓存继续提供敏感配置。已下载到客户端的内容不受服务器缓存策略控制，远端凭证泄漏仍需要远端轮换。

# 9. Runner、任务系统与测试设计

## 9.1 Runner 信任与执行接口

Runner 通过 mTLS 连接 API 内部监听器，自报可执行 core_build_id、架构与可用槽位。服务器以登记的证书身份和能力清单过滤任务，不相信请求自报即可执行任意构建。

任务类型枚举为 config_validate、connectivity、download_throughput；来源刷新和纯编译由 API 后台 Worker 执行，不下发任意 shell 或任意 URL 给 Runner。P1 可增加 udp_probe、upload_throughput 与匹配平台的验证器。

任务载荷包含冻结配置、核心构建、受控目标、超时、预算、预期响应及执行策略；仅允许当前任务所需数据。Runner 验证载荷摘要与版本后，将明文写入 0700 目录中的 0600 文件。回传结果和日志必须脱敏。

## 9.2 持久队列与租约

API 负责在短事务中使用 FOR UPDATE SKIP LOCKED 领取可执行任务，再写入 runner_id、lease_seq 和 lease_until。该 SQL 模式适用于队列式并发消费者，不用于要求完整一致视图的一般业务查询。[R19]

状态转移：queued→leased→running→succeeded/failed/canceled/timed_out。running 可以产生 pass/fail/inconclusive 结论；任务完整执行并得到节点失败属于 succeeded＋fail。取消为持久字段 cancel_requested_at，不仅是前端本地状态。

默认租约 30 秒、心跳 5 秒。每次重新发放任务递增 lease_seq，所有 heartbeat/event/result 请求同时携带 job_id、attempt、lease_seq。数据库使用条件更新，只接受当前有效租约；迟到结果返回 409 LEASE_LOST，不改变最终结果。

网络分区下无法数学上保证只执行一次。系统采用至少一次调度、租约 fencing 和结果幂等；Runner 收不到有效续租时须在本地租约期限前主动停止内核。配置检查可有限重试；连通性仅对执行基础设施故障最多重试一次；吞吐任务默认不自动重试，避免重复流量。

## 9.3 子进程生命周期

```text
领取任务 -> 校验构建/载荷/期限 -> 预留槽位并核验预算预留
  -> 准备独立临时目录和受控端口
  -> 原生配置检查 -> 启动内核
  -> 等待本地入口真实就绪 -> 经代理执行探测
  -> 记录结果 -> 关闭测试连接
  -> TERM 进程组 -> 限期后 KILL -> Wait 回收
  -> 删除临时文件 -> 结算预算 -> 提交最终状态
```

Go 使用 exec.CommandContext 的固定二进制路径和参数数组，不经过 sh -c。清空继承环境，只放入明确白名单，例如固定 PATH、当前工作目录与必要资源路径；特别禁止继承代理环境、调试、自动下载和脚本相关变量。Mihomo 的命令行存在 post-up/post-down 功能，因此环境和参数都必须被控制。[R14]

端口从 Runner 管理的范围分配并在进程启动后验证监听归属；不能“取一个随机空闲端口”后假设没有竞争。若内核无法继承预绑定文件描述符，则采用锁定端口分配＋有限冲突重试，并把启动失败作为基础设施错误。测试入口只绑定 127.0.0.1，必要时使用随机本地认证，控制 API 默认不开启。

取消、超时和崩溃使用统一 defer 清理路径，回收整个进程组及子进程。启动时扫描本进程记录的未结束任务，不能按可复用 PID 盲目杀进程；应同时校验进程启动标识或使用独立容器/cgroup 作为隔离单位。

## 9.4 内核检查和启动命令

下列为适配器应验证的基础命令形式；参数和资源路径必须在锁定构建上通过烟测。[R04][R08][R14]

```bash
xray run -test -c /work/job/config.json
sing-box check -c /work/job/config.json
mihomo -t -d /work/job -f /work/job/config.yaml

xray run -c /work/job/config.json
sing-box run -c /work/job/config.json
mihomo -d /work/job -f /work/job/config.yaml
```

所有路径由 Runner 创建，不接受用户传入任意路径。Xray 的配置参数也可支持远程配置，但本项目只允许本地生成文件，不使用其远程读取形式。[R04]

## 9.5 统一探测方法与指标

三个内核均生成受控 SOCKS5 入口，Go 通过统一 SOCKS5 拨号器建立 TCP 测试连接。HTTPS 探测验证目标证书、状态和响应特征，默认禁用跨站重定向、连接复用与压缩，以减少缓存和样本混杂。探测目标使用自有或明确许可的服务，禁止把公共服务当作无预算测速资源。

| 指标 | 精确定义 | 展示要求 |
| --- | --- | --- |
| config_check_ms | 启动检查进程到退出的单调时钟耗时 | 不是节点延迟 |
| core_start_ms | 启动到受控本地入口确认就绪 | 属于执行开销 |
| proxy_dial_ms | SOCKS 握手到目标连接建立，可能含多层连接开销 | 不命名为单纯服务端 TCP RTT |
| target_tls_ms | 经代理到 HTTPS 目标完成 TLS 握手的耗时 | 仅在可测时提供 |
| http_ttfb_ms | 发起 HTTP 请求到首个响应字节；记录计时边界 | 明确包含哪些连接阶段 |
| http_total_ms | 发起请求到预期小响应读完 | 连通性主指标，不称 ICMP Ping |
| body_bytes | 实際读取的响应体字节数 | 默认关闭压缩，避免膨胀量误导 |
| body_duration_ms | 从开始读取响应体到终止的持续时间 | 单调时钟；没有足够样本时不算吞吐 |
| throughput_mbps | body_bytes×8／body_duration_seconds／1,000,000 | 与 MiB/s 区分，注明时间/字节截断 |
| egress_ip | 自有目标返回的出口地址 | 只作诊断，不单独证明链方向 |

小响应延迟默认做 3 个独立请求，展示有效样本数、中位数及失败数，不在 3 个样本上显示貌似精确的 P95。吞吐默认 1 个受限样本；持续时间小于 1 秒或有效字节低于设定阈值时标为样本不足，不报告“最大带宽”。应用字节不包含协议、TLS 与重传开销，日预算需保留余量。

## 9.6 预算、并发与任务结果

创建测试时为首次 attempt 在 quota_buckets 锁行预留上限，写 quota_reservations；领取时复用该记录，不重复扣减。允许重试的任务必须为新 attempt 单独预留。拒绝超过当日总预算或并发槽位的请求；不允许多个批次各自只检查剩余额度而重复透支。日期桶使用 UTC，跨日任务按创建时预留桶结算，规则固定且可审计。

结束时原子结算实际应用层字节；丢失且无法确认用量的吞吐任务按预留上限保守结算。失败和取消同样计费。每个 attempt 只结算一次，重复结果按 result_hash 幂等处理。批量任务的剩余子任务在总预算耗尽时取消，不能继续后台跑满。

默认并发为连通性 4、吞吐 1；吞吐测试期间可降低其他网络探测并发，记录 CPU 限流、网络位置和当前负载。历史结果绑定 subject_revision 和 core_build_id，编辑节点后 UI 将旧结果标为过期。

## 9.7 错误分类

| 类别 | 示例错误码 | 是否重试 |
| --- | --- | --- |
| 输入或能力 | INVALID_CONFIG、CAPABILITY_UNSUPPORTED、DEPENDENCY_CYCLE | 不重试 |
| 配置验证 | CORE_CONFIG_INVALID | 不重试，定位字段与适配器 |
| 目标连接 | PROXY_CONNECT_FAILED、AUTH_FAILED、TARGET_TLS_FAILED、HTTP_EXPECTATION_FAILED | 结论为 fail；不自动当作基础设施错误 |
| 执行基础设施 | CORE_BINARY_MISMATCH、PORT_BUSY、RUNNER_RESOURCE_LIMIT | 有限重试；吞吐除外 |
| 控制与预算 | CANCELED、JOB_TIMEOUT、LEASE_LOST、BUDGET_EXCEEDED | 不自行重复执行 |
| 不可判断 | TEST_TARGET_UNAVAILABLE、INSUFFICIENT_SAMPLE | inconclusive；不损害节点历史评分 |

# 10. HTTP API、内部接口与错误契约

## 10.1 公共约定

管理 API 前缀 /api/v1；Runner 内部 API 前缀 /internal/v1，仅内部 mTLS 监听器启用。订阅路径 /s/{token}/{target_key} 独立鉴权，不沿用管理 Cookie。

JSON 字段使用 snake_case；时间为带时区 RFC3339，数据库用 UTC；枚举统一小写。列表使用 limit（默认 50、最大 200）和不透明 cursor，稳定排序按 created_at、id；游标绑定筛选条件摘要，不能跨筛选复用。响应包含 request_id，敏感字段默认缺省而不是伪造星号值。

写操作要求 If-Match: "r<N>"，创建操作除外；异步创建、发布、令牌签发和导入提交支持 Idempotency-Key。幂等作用域为调用主体＋路由＋key，保存请求 HMAC；同键不同请求返回 409。普通参数使用严格 JSON 解码，未知字段返回错误。

## 10.2 管理 API 清单

| 方法与路径 | 主要输入 | 输出／副作用 |
| --- | --- | --- |
| POST /setup | 一次性初始化凭证、管理员信息 | 仅空系统允许，创建管理员 |
| POST /auth/login；POST /auth/logout | 用户名/密码；当前会话 | 会话 Cookie 创建或吊销 |
| GET /auth/me；POST /auth/reauth | 当前会话；密码 | 用户信息、近期认证标记 |
| POST /imports | text 或受限文件、format、source_id | 202，导入预览任务／batch_id |
| GET /imports/{id}；POST /imports/{id}/commit | 候选决策和期望版本 | 逐条诊断；事务提交选择项 |
| GET/POST /sources；GET/PATCH/DELETE /sources/{id} | 来源配置、刷新策略 | 来源元数据，秘密不回显 |
| POST /sources/{id}/refresh | 幂等键 | 202，刷新任务 |
| GET/POST /nodes；GET/PATCH/DELETE /nodes/{id} | 节点类型化字段、版本 | 节点列表/修订；删除为软删除 |
| POST /nodes/batch | 操作类型和 node_ids | 批量标签、启停；逐项结果 |
| GET /nodes/{id}/revisions；GET /nodes/{id}/references | 分页 | 脱敏修订和被引用影响 |
| POST /nodes/{id}/reveal | 近期再认证 | 受审计的一次性秘密读取 |
| GET/POST /chains；GET/PATCH/DELETE /chains/{id} | 两跳引用和版本 | 类型化 Chain |
| GET/POST /policy-groups；GET/PATCH/DELETE /policy-groups/{id} | 策略、成员、健康检查 | 策略组及兼容诊断 |
| GET/POST /routing-profiles；GET/PATCH/DELETE /routing-profiles/{id} | 有序规则和 final | 路由修订 |
| GET/POST /dns-profiles；GET/PATCH/DELETE /dns-profiles/{id} | 解析器与依赖 | DNS 修订和循环诊断 |
| GET/POST /rule-sets；GET/PATCH/DELETE /rule-sets/{id} | 标准文本规则 | 规则修订及行号错误 |
| GET /client-presets；GET /capabilities | 目标构建与字段约束筛选 | 审核过的预设、能力及证据 |
| GET/POST /subscriptions；GET/PATCH/DELETE /subscriptions/{id} | 成员、目标、路由和 DNS | 订阅方案；删除先阻断获取 |
| POST /subscriptions/{id}/clone | 新名称、If-Match | 创建新方案，保留资源引用，不复制令牌或发布 |
| POST /subscriptions/{id}/compile | 目标集合、期望方案修订 | 202，compile_batch_id |
| GET /compile-batches/{id} | 无 | 进度、脱敏预览、依赖和诊断 |
| POST /subscriptions/{id}/publish | batch_id、期望当前 generation | 201，新 publication |
| POST /subscriptions/{id}/rollback | 历史 publication_id、期望 generation | 安全检查后生成新发布事件 |
| GET /subscriptions/{id}/publications | 分页 | 历史发布和阻断原因 |
| POST /subscriptions/{id}/tokens；GET /subscriptions/{id}/tokens | 允许目标、到期；分页 | 首次返回原令牌；列表只返回元数据 |
| POST /tokens/{id}/revoke | 原因 | 事务吊销，立即用于后续授权 |
| POST /exports | 资源／发布物、格式、是否含秘密 | 普通脱敏导出或再认证后的完整导出 |
| GET/POST /test-targets；GET/PATCH/DELETE /test-targets/{id} | 管理员登记目标、预期响应、限额和版本 | 目标需先通过安全检查，停用阻止新测试 |
| POST /tests | 被测对象、构建、类型、登记目标、预算 | 202，父批次与子任务 ID |
| GET /jobs；GET /jobs/{id}；POST /jobs/{id}/cancel | 筛选；取消原因 | 持久状态、结论和取消请求 |
| GET /jobs/{id}/events | Last-Event-ID | SSE 阶段事件与重放 |
| GET /test-results | 对象、构建、位置、时间 | 可追溯测试记录 |
| GET /cores；POST /cores/{id}/disable | 构建清单；停用原因 | 构建状态；阻断相应不安全目标 |
| GET/PATCH /system/settings；GET /audit-events | 白名单配置；分页筛选 | 限额、保留、脱敏审计 |

上表中 GET/POST 集合路径与 GET/PATCH/DELETE 单对象路径是接口组表示法，实际 OpenAPI 中逐项定义。P0 不提供任意二进制上传、远程可执行文件安装、数据库管理和任意命令执行接口。数据库迁移、密钥恢复与备份恢复采用受控主机运维流程。

## 10.3 关键请求与响应

创建链请求：

```json
{
  "name": "第一跳 A / 出口 B",
  "hops": [
    {"node_id": "11111111-1111-4111-8111-111111111111"},
    {"node_id": "22222222-2222-4222-8222-222222222222"}
  ],
  "failure_policy": "fail_closed"
}
```

创建测试请求只引用管理员已登记的 test_target_id，不允许普通调用者传任意 URL：

```json
{
  "subjects": [
    {"kind": "chain", "id": "77777777-7777-4777-8777-777777777777"}
  ],
  "core_build_id": "55555555-5555-4555-8555-555555555555",
  "type": "download_throughput",
  "test_target_id": "88888888-8888-4888-8888-888888888888",
  "limits": {"duration_ms": 10000, "max_bytes": 20971520}
}
```

提交时服务端冻结 subject_revision，并返回最终采用的限制；不能因为用户传入更大的预算而提升系统上限。创建请求返回 202 和 job_ids；资源创建返回 201、资源 ID、revision 与 ETag。

标准错误响应：

```json
{
  "error": {
    "code": "DEPENDENCY_EXCLUDED",
    "message": "必要链节点被显式排除，无法发布",
    "details": [
      {"field_path": "/members/exclude_ids/0", "resource_id": "node-id"}
    ]
  },
  "request_id": "request-id"
}
```

## 10.4 状态码与错误码

| HTTP | 含义 | 示例 |
| --- | --- | --- |
| 400 | JSON、路径或请求形态错误 | MALFORMED_REQUEST、UNKNOWN_FIELD |
| 401 | 管理会话无效 | AUTH_REQUIRED、SESSION_EXPIRED |
| 403 | 已认证但无操作权限 | PERMISSION_DENIED、REAUTH_REQUIRED |
| 404 | 不存在或不应泄漏存在性 | RESOURCE_NOT_FOUND、公开订阅无效令牌 |
| 409 | 业务状态、幂等或租约冲突 | COMPILE_OBSOLETE、IDEMPOTENCY_CONFLICT、LEASE_LOST |
| 412 | If-Match 版本不匹配 | REVISION_MISMATCH |
| 413 | 输入或解码结果过大 | INPUT_LIMIT_EXCEEDED |
| 422 | 结构正确但领域语义无效 | CAPABILITY_UNSUPPORTED、DNS_CYCLE、EMPTY_GROUP |
| 428 | 必需的版本前置条件缺失 | PRECONDITION_REQUIRED |
| 429 | 请求频率、并发或预算超限 | RATE_LIMITED、BUDGET_EXCEEDED |
| 503 | 数据库、执行能力或有效订阅不可用 | SUBSCRIPTION_BLOCKED、RUNNER_UNAVAILABLE |

订阅客户端可读错误为简短文本或结构化错误，而不是代理配置。所有错误和日志必须限制长度，不把内核原始 stderr 不加处理地返回。

## 10.5 Runner 内部接口和事件

| 接口 | 约束 |
| --- | --- |
| POST /internal/v1/runners/heartbeat | 证书身份匹配，报告构建与资源摘要 |
| POST /internal/v1/jobs/lease | 服务端匹配能力并分配任务，不接受任意 job_id 领取 |
| POST /internal/v1/jobs/{id}/heartbeat | 校验 runner、attempt、lease_seq、期限；返回取消状态 |
| POST /internal/v1/jobs/{id}/events | 只接受当前租约；事件去重、长度限制与脱敏 |
| POST /internal/v1/jobs/{id}/result | 结果摘要幂等；当前租约和预算原子结算 |

SSE 事件包含 job_id、seq、phase、completed、total、verdict 和脱敏错误摘要。客户端通过 Last-Event-ID 重放；事件保留窗口外返回“重新读取任务快照”信号。不要把 stdout 全量流式转发给浏览器，也不要把 SSE 当成任务事实源。

# 11. 前端实现设计

## 11.1 页面与路由

```text
/login
/dashboard
/nodes                 /nodes/:id
/sources               /sources/:id
/chains                /chains/:id
/policy-groups
/routing               /dns                 /rule-sets
/subscriptions         /subscriptions/:id
/jobs                  /jobs/:id
/cores                 /settings            /audit
```

以服务端分页查询为主，Pinia 仅保存会话状态、短生命周期草稿、选中项和 UI 状态，不把全节点库及明文秘密放入持久化 localStorage。请求缓存按资源 ID＋revision 索引，发生写入或安全事件后失效。

## 11.2 动态表单和能力反馈

字段表单来自受版本控制的前端 Schema 映射，服务端仍独立校验。protocol、transport、security 为判别条件，改变协议时清除或确认不再适用的字段，避免旧表单隐藏字段被再次提交。

节点编辑显示“已保存秘密”“未配置”状态，秘密输入只在修改时提交。兼容结果分别显示内核构建、客户端导入状态及原因。安全设置如关闭证书校验默认不允许，特殊启用必须有警告及审计，不能由导入方言自动静默关闭。

## 11.3 编排和发布向导

链编辑使用两张有序节点卡片及“第一跳／最终出口”文字，不仅靠箭头颜色。界面同时显示链可用性和两个原节点的独立结果，避免用户误以为两个节点可用等于链可用。

订阅向导分为成员→路由与 DNS→输出目标→依赖与差异→校验→发布。最后一步列出显式成员、隐藏依赖和将分发的认证信息类别；多目标策略覆盖以并排差异展示。没有 effective_preview_hash 对应的确认不能提交发布，避免预览后输入变化的误确认。

发布后展示固定 generation、更新时间、目标状态、令牌操作和最近变更。只要草稿或依赖变化，显示“待重新编译”，不在后台自动改变已发布内容。安全阻断单独显示 blocked 和原因。

## 11.4 任务与错误体验

任务详情区分 queued、starting、validating、probing、cleaning、finished 的执行阶段，最终同时展示 task_state 与 verdict。请求成功但节点失败不是后台红色崩溃页；样本不足或测试目标异常显示“无法判定”。

SSE 使用指数退避与抖动重连，按 seq 去重，重新连接先获取任务快照。用户刷新页面、关闭页面不会取消服务器任务；取消必须调用明确接口。并发编辑产生 412 时展示本地草稿与最新修订差异，不自动覆盖。

前端上线门槛包括基本键盘操作、表单标签、清晰错误文本、深浅主题对比和中文长名称布局。非管理员接口错误不泄漏资源是否存在，敏感页面不加载第三方分析脚本。

# 12. 安全与隐私设计

## 12.1 威胁模型与防护位置

| 威胁 | 入口 | 主要控制 | 剩余风险 |
| --- | --- | --- | --- |
| 未授权管理访问 | 登录、管理 API | 会话、CSRF、权限、重认证、限流 | 主机被攻破后应用权限不能保护全部数据 |
| 订阅凭证泄漏 | URL、日志、浏览器引用、剪贴板 | 独立高熵令牌、日志模板化、no-store、撤销 | 已下载节点信息不能远程收回 |
| SSRF/重绑定 | 来源、规则、节点地址 | 受限抓取、解析后校验、绑定 IP、内核出站防护 | 仅应用校验不能防御被攻破内核任意联网 |
| 原生配置注入 | 导入、扩展、模板 | 类型化模型、白名单、生成后安全检查 | 上游解析器漏洞仍需容器与版本治理 |
| 任意执行或持久化 | 命令参数、环境、文件路径 | 固定二进制、无 shell、清环境、受限目录 | 同 UID 的可信内核不是强隔离沙箱 |
| 配置降级泄漏 | 策略回退、链失败 | 显式默认拒绝、故障注入和负例测试 | 用户显式配置直连需自行确认业务意图 |
| 资源滥用 | 超大导入、循环依赖、批量测速 | 深度/数量/时间/并发/字节预算 | 网络协议开销与丢失任务用量需保守计量 |
| 旧备份复活权限 | 恢复数据库 | auth_epoch 重置、所有令牌/会话/Agent 凭证吊销 | 旧备份仍含旧节点秘密，需控制备份访问 |

网络输入防御遵循 OWASP 对域名/IP、跳转和 DNS 重绑定的分层检查思路；本文的具体阈值和拒绝策略是项目设计。[R16]

## 12.2 管理员认证与会话

建议使用 Argon2id 存管理员密码哈希，参数初始为内存 64 MiB、迭代 3、并行 1，并按部署主机实测成本调整。每个密码独立随机 salt，记录算法和成本版本；登录成功后按需要升级哈希。管理员密码不能用可逆加密或快速 SHA-256 代替密码哈希。[R17]

会话使用随机不透明 ID，Cookie 为 HttpOnly、Secure、SameSite=Lax、Path=/；不写 localStorage。管理变更请求同时验证 CSRF token 和 Origin，信任的反向代理来源显式配置，不信任任意 X-Forwarded-For。初始会话绝对有效期 8 小时、空闲 30 分钟，敏感操作要求最近 5 分钟内重新认证。

登录失败按账户与可信源 IP 双层限速，避免单个账号被恶意永久锁死。修改密码递增 auth_version 并吊销旧会话；初始化口令只从受控主机生成或一次性文件读取，不在公开日志反复输出。

## 12.3 可还原秘密的加密

节点认证、来源请求头、来源 URL、导入原文、冻结载荷和发布产物采用带认证的信封加密。每个加密记录使用随机数据密钥和随机 nonce，数据密钥由主密钥包裹；AAD 包含 scope、表名、对象 ID、修订和 Schema 版本，阻止密文在对象间被无声替换。[R18]

主密钥通过只读文件或外部秘密管理注入，不放镜像、不和数据库备份捆绑。token_pepper、内容去重 HMAC 密钥与数据主密钥独立，不复用原始密钥跨不同用途。备份必须注明需要哪些 key_id 才能恢复。

轮换主密钥优先重新包裹数据密钥，不改变业务修订、配置内容摘要和节点安全 epoch；代理认证内容轮换则是业务安全变更，必须新增节点修订并阻断旧发布。Go 内存无法保证所有字符串拷贝被可靠擦除，因此应减少明文驻留、禁止调试转储并控制进程权限，而不是承诺完美内存清零。

## 12.4 内核在线执行的网络防护

SafeFetcher 只能保护 Go 发起的来源请求，不能替代内核进程的网络限制。P0 在测试载荷生成时拒绝第一跳的受保护 IP，解析后冻结已批准连接 IP，并保留 TLS/SNI/Host 所需信息。核心若还需要未经校验的自动解析、远程资源或替代地址，必须改为经批准的解析预设或拒绝在线测试。

对于公网开放或多租户部署，应采用隔离执行网络和主机级出站 ACL：仅允许任务所需第一跳、受控解析器、测试服务和 Runner 控制接口；禁止数据库网段、元数据、宿主机管理接口及其他内部服务。该 ACL 由运维在隔离层实施，不能赋予 API 或普通 Runner NET_ADMIN 去动态修改整台宿主机防火墙。

P0 Compose 使用可信管理员＋固定内核二进制＋非 root 进程的风险模型，不宣称按任务创建了强沙箱。Runner 与同容器内核若使用相同 UID，环境清理不阻止内核读取该 UID 可读文件。面对不可信配置或多租户，必须把监督进程凭证移出任务容器，使用独立网络/文件系统/身份或更强沙箱后才开放。

## 12.5 文件、日志、前端与供应链

只允许任务目录内相对资源路径，拒绝 ..、绝对路径、符号链接逃逸和远程配置来源。临时目录为 tmpfs、noexec、nosuid，并限制容量；注意删除 tmpfs 文件不等于可证明擦除内存。日志分对象截断，每任务初始上限 256 KiB，先做结构化脱敏再存储。

发布流水线扫描依赖、容器和秘密，产出 SBOM、镜像摘要、二进制摘要和许可证清单。内核从固定官方来源/提交构建或下载，校验预登记摘要；不能让管理员通过页面输入任意二进制 URL 并立即执行。

分发时核对具体锁定版本的许可证和名称使用条款。官方源码显示 Xray 使用 MPL-2.0、Mihomo 使用 GPL-3.0，sing-box 许可证声明 GPL-3.0-or-later 并包含附加说明。实际源码提供、修改和组合分发义务应结合分发方式审查；进程分离不能被当作自动免除义务的依据。[R22][R23][R24]

# 13. Docker 部署与配置设计

## 13.1 部署拓扑

推荐三个常驻服务：api、runner、postgres；增加一次性 migrate 服务执行迁移。api 镜像包含独立构建的静态前端。外部反向代理终止 HTTPS 并将请求转发到仅绑定宿主机回环的 8080。Runner 通过独立内部 mTLS 端口 9091 访问 API。

数据库只加入 data 内部网络；Runner 不加入 data，只加入 control 和用于授权外联的 egress 网络。API 加入 data、control 和必要前端网络。网络分段减少暴露面，但 Docker bridge 本身不是完整出站访问策略。[R20]

健康检查区分 /healthz（进程活着）和 /readyz（数据库、迁移、密钥可用）。Runner 离线不会导致已有安全有效订阅不可读，但新编译校验和测试不可完成。API 不应把外网测试目标故障当作整个服务存活失败。

## 13.2 镜像构建与版本锁

API 使用前端构建阶段＋Go 构建阶段＋非 root 运行阶段。Runner 单独包含 Go 执行器和三个固定内核，按 linux/amd64、linux/arm64 生成对应镜像；运行镜像包含必要 CA 证书和受控资源。明确 CGO 与动态库要求，不假设所有内核都能在任意极简镜像中执行。

compat/cores.lock.yaml 至少保存内核家族、版本、提交、OS/arch、构建特性、二进制 SHA-256、来源、适配器版本、夹具集版本和验证日期。下载摘要由发布流程维护，不能在运行时从同一个不可信 URL 现取“可信校验值”。

所有镜像用不可变摘要部署，数据库大版本单独升级，不随应用容器无计划升级。Compose 的 PGDATA 路径按所选固定镜像明确设置；下例采用显式目录，避免依赖不同数据库镜像版本的默认数据路径。

## 13.3 Compose 设计样例

以下是应随项目实现的部署契约示例，并非已经存在的镜像。PSB_API_IMAGE、PSB_RUNNER_IMAGE、POSTGRES_IMAGE 必须填入实际构建并测试的 image@sha256 引用；API/Runner 的子命令和 *_FILE 配置读取逻辑须按此契约实现。示例采用回环端口，需要另配 HTTPS 反向代理。

```yaml
services:
  postgres:
    image: ${POSTGRES_IMAGE:?set_a_tested_image_digest}
    environment:
      POSTGRES_DB: psb
      POSTGRES_USER: psb
      POSTGRES_PASSWORD_FILE: /run/secrets/db_password
      PGDATA: /var/lib/postgresql/psb-data
    secrets: [db_password]
    volumes:
      - pgdata:/var/lib/postgresql
    networks: [data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U psb -d psb"]
      interval: 5s
      timeout: 3s
      retries: 20
    restart: unless-stopped

  migrate:
    image: ${PSB_API_IMAGE:?set_a_tested_image_digest}
    command: ["migrate", "up"]
    environment:
      PSB_DATABASE_DSN_FILE: /run/secrets/database_dsn
    secrets: [database_dsn]
    networks: [data]
    depends_on:
      postgres: {condition: service_healthy}
    restart: "no"

  api:
    image: ${PSB_API_IMAGE:?set_a_tested_image_digest}
    command: ["serve"]
    user: "10001:10001"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    environment:
      PSB_DATABASE_DSN_FILE: /run/secrets/database_dsn
      PSB_MASTER_KEY_FILE: /run/secrets/master_key
      PSB_TOKEN_PEPPER_FILE: /run/secrets/token_pepper
      PSB_CONTENT_HMAC_KEY_FILE: /run/secrets/content_hmac_key
      PSB_PUBLIC_URL: ${PSB_PUBLIC_URL:?set_https_origin}
      PSB_INTERNAL_TLS_CERT_FILE: /run/secrets/api_cert
      PSB_INTERNAL_TLS_KEY_FILE: /run/secrets/api_key
      PSB_RUNNER_CA_FILE: /run/secrets/runner_ca
      PSB_HTTP_ADDR: ":8080"
      PSB_INTERNAL_ADDR: ":9091"
    secrets:
      - database_dsn
      - master_key
      - token_pepper
      - content_hmac_key
      - api_cert
      - api_key
      - runner_ca
    ports: ["127.0.0.1:8080:8080"]
    networks: [front, data, control]
    tmpfs: ["/tmp:rw,noexec,nosuid,size=64m,uid=10001,gid=10001"]
    mem_limit: 1g
    cpus: "1.0"
    pids_limit: 128
    depends_on:
      migrate: {condition: service_completed_successfully}
    healthcheck:
      test: ["CMD", "/app/psb-api", "healthcheck"]
      interval: 10s
      timeout: 3s
      retries: 3
    restart: unless-stopped
    stop_grace_period: 20s

  runner:
    image: ${PSB_RUNNER_IMAGE:?set_a_tested_image_digest}
    command: ["serve"]
    user: "10002:10002"
    read_only: true
    init: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    environment:
      PSB_CONTROL_URL: https://api:9091
      PSB_CONTROL_CA_FILE: /run/secrets/runner_ca
      PSB_CLIENT_CERT_FILE: /run/secrets/runner_cert
      PSB_CLIENT_KEY_FILE: /run/secrets/runner_key
      PSB_WORK_DIR: /work
    secrets: [runner_ca, runner_cert, runner_key]
    networks: [control, egress]
    tmpfs:
      - /work:rw,noexec,nosuid,size=256m,uid=10002,gid=10002
      - /tmp:rw,noexec,nosuid,size=64m,uid=10002,gid=10002
    mem_limit: 2g
    cpus: "2.0"
    pids_limit: 256
    depends_on:
      api: {condition: service_healthy}
    healthcheck:
      test: ["CMD", "/app/psb-runner", "healthcheck"]
      interval: 10s
      timeout: 3s
      retries: 3
    restart: unless-stopped
    stop_grace_period: 20s

networks:
  front: {}
  data: {internal: true}
  control: {internal: true}
  egress: {}

volumes:
  pgdata: {}

secrets:
  db_password: {file: ./secrets/db_password}
  database_dsn: {file: ./secrets/database_dsn}
  master_key: {file: ./secrets/master_key}
  token_pepper: {file: ./secrets/token_pepper}
  content_hmac_key: {file: ./secrets/content_hmac_key}
  api_cert: {file: ./secrets/api.crt}
  api_key: {file: ./secrets/api.key}
  runner_ca: {file: ./secrets/runner-ca.crt}
  runner_cert: {file: ./secrets/runner.crt}
  runner_key: {file: ./secrets/runner.key}
```

Compose secrets 以文件提供并按服务授权挂载，但本地 Compose 不是自动加密保管库；宿主机文件权限和备份依旧由运维负责。文件型 secret 的 UID/GID 行为也应在实际 Compose 环境核对，确保对应非 root UID 能读取被授权文件，同时其他主体不可读。[R21]

depends_on 的 service_healthy 和 service_completed_successfully 用于启动顺序，不能替代运行中的断线重连和故障恢复。[R27] 生产落地还须给 Postgres 设置内存/连接上限，并配置各容器日志轮换，避免未限制的数据库与日志占满宿主机。实际数据库应区分迁移与运行账号，运行账号不具备建表或高权限操作；上例为易读的单 DSN 结构，生产需替换为独立最小权限 DSN。

## 13.4 配置清单与默认值

| 配置 | 默认／要求 | 生效方式 |
| --- | --- | --- |
| 管理端外部地址 | 必填 HTTPS URL | 启动配置；用于安全 URL 和 CSRF |
| 数据库 DSN、主密钥、token pepper、内容 HMAC 密钥 | 文件注入，缺失即启动失败 | 不允许 API 普通接口读取 |
| 三内核版本与摘要 | 从镜像构建清单读取并核验 | 不允许运行时任意下载 |
| Source 超时/大小 | 15 s、解码后 10 MiB、最多 3 跳 | 管理配置版本化 |
| 配置展开限额 | 2,000 出站、20,000 规则、10 MiB | 编译前及序列化后双重检查 |
| 测试并发 | 连通性 4、吞吐 1 | 全局队列和本机槽位同时执行 |
| 测试预算 | 单样本 10 s 或 20 MiB；日预算 1 GiB | 不接受请求提升系统限制 |
| 租约/心跳 | 30 s／5 s | 心跳使用服务端时间及本地单调期限 |
| 运行身份 | API 10001，Runner 10002 | 镜像与挂载文件权限一致 |
| 高权限功能 | TUN、NET_ADMIN、Docker Socket 默认禁止 | 未来专用模式单独设计 |

## 13.5 启动、升级和回退

首次部署：创建秘密文件与内部 CA/证书→填入固定镜像→校验 Compose→启动数据库与迁移→启动 API→本地主机完成一次性管理员初始化→登记 Runner→核验三内核→配置受控测试目标→执行烟测。CA 证书包含 api 的正确服务名，不能通过关闭证书校验解决连接问题。

升级前生成备份并验证 key_id 完整；先在测试环境执行 migration、兼容夹具和原始 API 契约测试，再滚动替换应用/Runner。迁移采用 expand/contract：先新增兼容列与读写路径，再在后续版本删除旧结构。

应用回退仅回到与当前 Schema 兼容的镜像，不自动执行破坏性 down migration。Runner 新构建先以少量任务验证，已有发布引用旧构建的校验记录不会被悄悄重写。安全禁用的构建不允许因镜像回退重新开放。

# 14. 可观测性、备份与运行手册

## 14.1 日志、指标和审计

日志为结构化事件，含 request_id、job_id、resource_id、revision、target_key、core_build_id、phase 和 error_code，不含节点密码、完整分享链接和真实令牌。所有内核日志先在 Runner 脱敏，再在 API 接收端再次限额和扫描，避免单一过滤器失效。

建议指标包括 API 延迟/错误率、订阅读取与拒绝原因、编译各阶段耗时、能力拒绝数量、队列长度、租约过期、Runner 心跳、进程数、RSS、预算预留/使用量、源刷新成功率、数据库连接与磁盘容量。Prometheus 类指标标签不得使用 node_id、job_id 或原 URL 等高基数字段；这些只进入日志和可查询记录。

审计与普通日志分开。审计覆盖秘密导出、令牌签发/撤销、节点安全变更、发布/回滚、构建停用、配额变更和恢复。审计只能保证应用正常边界内的可追溯，不声称本地数据库管理员无法篡改；更强需求可将摘要或事件同步到外部只追加存储。

## 14.2 告警与故障处理

| 故障 | 检测 | 处理原则 |
| --- | --- | --- |
| 数据库不可用 | readyz、连接错误和授权失败 | 返回 503，不从旧授权缓存提供配置；停止发新任务 |
| Runner 全部离线 | 心跳超时和队列增长 | 保留有效订阅读取；暂停新校验/测试并提示容量不足 |
| 来源连续失败 | 来源错误计数和最近成功时间 | 保留旧来源快照；退避重试；不空覆盖 |
| 发布失败率增加 | 编译/内核错误按版本聚合 | 停止新构建推广，回滚兼容版本，定位差异 |
| 临时盘或内存逼近限额 | tmpfs/RSS、任务失败码 | 降低并发，拒绝新任务，回收孤儿进程 |
| 令牌或节点泄漏 | 审计、访问异常或人工报告 | 撤销订阅，停用节点并轮换远端凭证，再重新发布 |
| 测试目标不可用 | 自有目标独立健康检查 | 标记 inconclusive，不批量判所有节点失效 |

## 14.3 备份、恢复与权限防复活

每日做 PostgreSQL 逻辑或物理备份，结合实际规模选择方案；备份加密、异地或离线保留，定期校验可恢复性。单独保管主密钥/key_id、构建清单、部署文件和内部 CA。RPO 24 小时、RTO 2 小时是拟定演练目标，实际取决于备份执行和机器资源。

恢复在隔离环境进行：停止对外下载→恢复数据库→提供匹配主密钥→执行迁移兼容检查→重置 scope.auth_epoch→吊销旧会话、订阅令牌和 Runner 证书登记→把非终态任务标记为失败或待人工确认→验证解密和已发布字节→重新登记 Runner→签发新订阅令牌→恢复访问。

旧备份可能保存已经被撤销的旧令牌；因此默认全量重置访问凭证，而不是相信旧库的 revoked_at。需要保留令牌可用性的高级恢复方案必须引入备份之外的撤销事实源，P0 不支持该模式。

## 14.4 保留与清理

执行需求文档的保留周期，并按引用可达性清理，不能先删除资源再让编译失败。job_events 可按时间删除，但最终 job 和 result 状态保留到结果周期结束。活动发布密文及其全部依赖必须保留，即使超出普通版本数量上限。

对长期增长的 test_results、job_events、audit_events 可在达到实际规模后增加时间分区；首期先记录体积和查询延迟，不凭预测提前拆库。清理作业本身可观测、可暂停、有执行预算，不能长事务锁住订阅读取。

# 15. 测试体系与质量门槛

## 15.1 测试分层

| 层级 | 验证内容 | 强制例子 |
| --- | --- | --- |
| 单元与属性测试 | URI 解析、标准化、引用、策略、预算和权限 | 编解码保持语义、重复输入稳定、无静默字段丢失 |
| 模糊测试 | URI、Base64、JSON/YAML、规则文本 | 深层嵌套、畸形转义、重复键、超长字符串不崩溃 |
| 编译 Golden 测试 | 固定 IR 对应确定目标字节 | 改名不改身份、标签冲突、链共享、目标差异 |
| 原生配置检查 | 三锁定构建读取完整生成配置 | 正负样例、弃用字段、缺少构建功能 |
| 受控网络集成 | 真内核与真测试服务验证行为 | A→B 方向、B 仅允许 A、全故障无直连 |
| 数据库并发 | 版本、租约、预算、发布和安全撤销 | 重复幂等键、过期 lease、并发编辑和撤销 |
| 前端 E2E | 导入→链→订阅→发布→测试 | 字段错误、版本冲突、SSE 重连、遮罩秘密 |
| 安全与恢复 | SSRF、配置注入、秘密扫描、旧备份恢复 | IPv6 映射、DNS 重绑定、路径逃逸、权限防复活 |
| 性能与耐久 | NFR 基准、长任务、崩溃恢复 | 稳态负载、重复取消、磁盘限额和重启 |

## 15.2 核心夹具清单

每个协议样例必须记录协议变体、加密、传输、安全选项、UDP 条件、目标内核构建和客户端预设。正例与反例成对维护，不以生成结果“长得正确”代替内核校验。

关键链夹具包括：A、B 单独可用但 A 无法承载 B；B 仅接受 A；交换 A/B；共享 B 的不同链；链处于策略组内；两个链出口同名；第一跳失效；B 认证错误；DNS 引导自环；同一组标签前缀误匹配；配置中 direct 存在但未被故障路径使用。

网络夹具可在受控 CI Docker 网络内运行代理服务端与 HTTP 测试端，此时私网访问只通过明确标记的 test-only 策略启用，不能复用到生产任务。断言同时检查目标响应、A/B 日志与 Runner 直连出口；仅出口 IP 不足以验证完整路径。

## 15.3 发布物验收

严格执行 SRS 的 AC-01～AC-24，并补充兼容清单每个 verified 项的自动化证据。任何新的协议、传输、DNS 方案或内核构建必须新增样例与回归，不能只更新前端下拉框。

Go 测试覆盖率目标为核心解析/编译包语句覆盖率至少 80%；更重要的是关键不变量覆盖率为 100%：不静默直连、不泄漏凭证、稳定引用、确定性编译、原子发布、实时撤销、任务预算与租约 fencing。

性能报告必须注明机器配置、数据库版本、内核构建、数据集、并发、持续时间和排除条件。下载样本结果不用于证明 API 吞吐；模拟网络或架构仿真结果必须标注。文档中的所有性能值均为目标，实施后以实际报告取代。

## 15.4 CI/CD 流水线

```text
提交/合并请求
 -> 格式化、静态分析、Go/TS 单元测试
 -> Schema 与 OpenAPI 兼容检查
 -> URI 模糊/属性测试与编译 Golden
 -> 数据库迁移和并发测试
 -> 三内核配置检查与受控链路测试
 -> 前端构建与关键 E2E
 -> 两架构镜像构建、SBOM、依赖/秘密扫描
 -> 烟测、版本锁与证据归档
 -> 签名/固定摘要发布
```

公网测速不作为常规 CI 强制门槛，避免外网波动和流量费用让构建不可复现。新内核版本通过测试后建立新的 CoreBuild，而不是原地替换旧 ID 指向的二进制。

# 16. 实施拆分、交付物与演进

## 16.1 工作包

| 工作包 | 主要产物 | 前置依赖 |
| --- | --- | --- |
| WP-01 工程与安全基座 | Go/Vue 工程、登录、配置、加密、迁移、OpenAPI | 无 |
| WP-02 领域与导入 | IR Schema、资源修订、URI 解析、来源和覆盖 | WP-01 |
| WP-03 编排 | Chain、策略组、路由、DNS、依赖图 | WP-02 |
| WP-04 三内核编译 | 能力清单、Adapter、确定性输出、Golden | WP-02、WP-03 |
| WP-05 订阅发布 | 冻结、目标产物、原子发布、令牌与安全检查 | WP-04 |
| WP-06 Runner | 构建登记、租约、进程回收、连通性和预算测速 | WP-01、WP-04 |
| WP-07 管理体验 | 导入预览、编排向导、发布差异、任务中心 | 与 WP-02～WP-06 并行 |
| WP-08 运维与验收 | Docker、备份恢复、监控、威胁测试、性能报告 | 前述工作包 |

不在未确定团队规模和现有代码基础的情况下给出固定工期承诺。估算应根据工作包、已验证协议数量、客户端数量和自动化覆盖程度拆分，而不是只按页面数量计算。

## 16.2 实施完成时应交付的工程文件

必须包括源码与构建说明、OpenAPI 文件、IR Schema、全部数据库迁移、Dockerfile/Compose、版本与构建锁、测试夹具、兼容矩阵及证据、受控测速目标说明、备份与恢复手册、升级回退流程、威胁模型、许可证/SBOM 和已知限制。

本次交付的是两份设计文档及其可编辑源文件，不包含上述尚未实现的运行工程。第 17 章为核心实现契约示例，必须经开发、迁移测试和内核夹具验证后才能作为正式代码使用。

## 16.3 演进路线

P1 按组合增加 Hysteria2、TUIC、AnyTLS、WireGuard、特定传输、远程规则集与客户端预设；每次扩展先实现 IR 与能力证据，再开放 UI。复杂路由和 DNS 同样需要语义测试，不能直接搬运原生字段。

远程 Agent 沿用 Runner 证书和租约协议，但必须增加地理位置声明、信任级别、结果可信度和密钥最小化；多租户则需要独立授权模型、资源/网络隔离、秘密访问策略和租户配额，不能仅把 scope_id 参数暴露给用户。

三跳以上与动态策略链需要新增图复杂度限制和内核行为验证；跨内核桥接需要同时管理多个进程及入口出口生命周期，属于另一执行模式，不能输出成伪装的单内核订阅。

## 16.4 实施前冻结清单

| 项目 | 默认／已决定 | M0 需补充记录 |
| --- | --- | --- |
| 业务形态 | 单工作空间、可信管理员 | 公开访问边界和网络防护方案 |
| 数据与后端 | PostgreSQL、Go、独立 Runner | 语言/库/数据库的锁定版本 |
| 内核 | Xray、sing-box、Mihomo 独立二进制 | 精确版本、摘要、特性、构建来源和许可证 |
| 客户端 | 三类通用完整配置预设 | 实际桌面/移动客户端名称及版本，未测试保持未验证 |
| 测试 | 自有/许可目标、双重预算、无隐式直连 | 目标地址、证书、预期响应及许可条件 |
| 安全与恢复 | 文件注入密钥、恢复时重置凭证 | 密钥保管人、备份位置和恢复演练记录 |

# 17. 核心实现契约示例

## 17.1 Go 编译器与内核适配接口

以下为接口设计片段；类型定义和构建适配需在工程内补齐。编译器不得直接联网，Runner 不接受 Adapter 返回任意 shell 字符串。

```go
package adapter

import "context"

type CoreFamily string

type Diagnostic struct {
    Code       string
    Severity   string
    ResourceID string
    FieldPath  string
    TargetKey  string
    Message    string
}

type FrozenInput struct {
    SchemaVersion int
    SnapshotID    string
    Payload       []byte // typed IR decoded and checked before use
}

type Target struct {
    Key            string
    CoreBuildID    string
    ClientPresetID string
    Format         string
}

type Artifact struct {
    ContentType string
    Bytes       []byte
    ContentHMAC []byte
}

type Compiler interface {
    Compile(ctx context.Context, input FrozenInput,
        target Target) (Artifact, []Diagnostic, error)
}

type CommandSpec struct {
    ExecutableID string   // resolved from immutable build registry
    Args         []string // no shell interpolation
    WorkingDir   string   // allocated by the runner
}

type CoreAdapter interface {
    Family() CoreFamily
    ValidateSpec(jobDir string) (CommandSpec, error)
    RunSpec(jobDir string) (CommandSpec, error)
    RedactLog(line []byte) []byte
}
```

生产实现中使用各协议的具体类型替代在业务层传递任意 map。Artifacts 在出进程持久化前加密；其 bytes 不得写日志。超时、预算、路径权限、环境白名单与进程回收由 Runner 公共执行框架负责，不能让某个 Adapter 自行绕过。

## 17.2 资源修订核心 DDL 片段

以下仅示范关键关系与约束，完整迁移需要覆盖第 4 章全部表、索引、审计权限和跨 scope/kind 约束触发器。

```sql
CREATE TABLE scopes (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  catalog_revision bigint NOT NULL DEFAULT 0,
  auth_epoch bigint NOT NULL DEFAULT 1
);

CREATE TABLE resources (
  id uuid PRIMARY KEY,
  scope_id uuid NOT NULL REFERENCES scopes(id),
  kind text NOT NULL CHECK (kind IN (
    'node', 'chain', 'policy_group', 'routing_profile',
    'dns_profile', 'rule_set', 'client_preset',
    'subscription_profile', 'source'
  )),
  name text NOT NULL,
  head_revision bigint,
  enabled boolean NOT NULL DEFAULT true,
  security_epoch bigint NOT NULL DEFAULT 1,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (scope_id, id)
);

CREATE TABLE resource_revisions (
  resource_id uuid NOT NULL REFERENCES resources(id),
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  envelope bytea NOT NULL,
  content_hmac bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (resource_id, revision)
);

ALTER TABLE resources ADD CONSTRAINT resources_head_fk
  FOREIGN KEY (id, head_revision)
  REFERENCES resource_revisions(resource_id, revision)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE resource_refs (
  source_id uuid NOT NULL,
  source_revision bigint NOT NULL,
  path text NOT NULL,
  target_id uuid NOT NULL REFERENCES resources(id),
  target_revision bigint,
  expected_kind text NOT NULL,
  PRIMARY KEY (source_id, source_revision, path),
  FOREIGN KEY (source_id, source_revision)
    REFERENCES resource_revisions(resource_id, revision),
  FOREIGN KEY (target_id, target_revision)
    REFERENCES resource_revisions(resource_id, revision)
);

CREATE INDEX resource_refs_reverse_idx
  ON resource_refs(target_id);
CREATE INDEX resources_scope_kind_idx
  ON resources(scope_id, kind, enabled)
  WHERE deleted_at IS NULL;
```

head_revision 允许事务创建过程暂时为空，但服务层必须在提交前设置；正式迁移应增加延迟约束触发器确保不存在已提交的无头资源。target_revision 为空时依赖资源存在性外键，非空时额外验证具体修订；expected_kind 与 scope 的一致性不能只由上述 SQL 片段保证。

## 17.3 租约领取与结果提交

以下 SQL 说明行锁与租约更新的原子性；调用前必须按 Runner 身份确定可执行 job kind/core_build_id，且能力筛选必须进入候选 SQL，不能只在领取后丢弃不匹配任务。

```sql
WITH picked AS (
  SELECT id
  FROM jobs
  WHERE state = 'queued'
    AND available_at <= now()
    AND cancel_requested_at IS NULL
    AND attempt < max_attempts
    AND kind = ANY($2::text[])
    AND core_build_id = ANY($3::uuid[])
  ORDER BY priority DESC, available_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE jobs j
SET state = 'leased',
    runner_id = $1,
    attempt = j.attempt + 1,
    lease_seq = j.lease_seq + 1,
    lease_until = now() + interval '30 seconds'
FROM picked
WHERE j.id = picked.id
RETURNING j.*;
```

该语句在短事务内运行；槽位校验和该 attempt 的预算预留校验必须在同一领取事务完成；复用已创建的预留，不重复扣减。结果提交条件必须包含 runner_id、attempt、lease_seq、非终态及有效期限；更新 0 行即视为租约丢失。预算结算和最终结果写入使用同一事务，重复 result_hash 返回已有结果。

来源刷新与编译任务不由外部 Runner 领取，其本地 Worker 使用独立队列过滤器；不能因为 core_build_id 为空就跳过对 Runner 任务的能力验证。

## 17.4 状态机伪代码

```text
Publish(batchID, expectedGeneration):
  begin transaction
  lock scope
  lock publication head for profile
  require head.generation == expectedGeneration
  require batch.state == ready
  require batch.catalog_revision == scope.catalog_revision
  require all dependency epochs and statuses are valid
  require all enabled target outputs passed native checks
  insert immutable publication and output references
  replace active publication head
  append audit record
  commit

ReadSubscription(token, target):
  verify token against primary database
  require current scope epoch and target permission
  read active publication
  require every dependency/current build is security-valid
  load exact target artifact bytes
  return private, no-store response
```

读取步骤不得先返回缓存字节再补查权限。第一份发布不存在 head 时，创建头记录也在同一事务中完成，并使用方案级锁避免并发创建两个 generation=1。

## 17.5 能力清单记录样例

```yaml
schema_version: 1
family: singbox
version: REQUIRED_AT_M0
arch: linux-amd64
binary_sha256: REQUIRED_AT_M0
adapter_version: REQUIRED_AT_M0
capabilities:
  - key: chain.two_hop.tcp
    state: unverified
    fixture_ids: [chain-a-to-b, chain-a-down-no-direct]
  - key: policy.round_robin
    state: unsupported
    reason: no_native_adapter_in_this_baseline
client_validation:
  state: unverified
```

此样例特意使用未验证状态和待锁定字段，避免把设计意图伪装成测试结果。正式构建流水线拒绝含 REQUIRED_AT_M0 的清单进入发布镜像。

# 18. 参考依据与维护规则

资料核对日期：2026-09-07。下列来源用于支持内核机制、格式、数据库并发及安全事实；本项目的领域模型、阈值、接口与运行流程是设计决策。动态官网和主分支源码不是最终构建锁，实施时需追加提交/版本与验证证据。

[R01] [Project X：Sockopt（拨号代理与 DNS 循环）](https://xtls.github.io/config/transports/sockopt.html)

[R02] [Project X：Outbound Proxy（proxySettings 与传输）](https://xtls.github.io/en/config/outbound.html)

[R03] [Project X：路由与负载均衡](https://xtls.github.io/config/routing.html)

[R04] [Project X：命令参数](https://xtls.github.io/document/command.html)

[R05] [sing-box：Dial Fields](https://sing-box.sagernet.org/configuration/shared/dial/)

[R07] [sing-box：URLTest](https://sing-box.sagernet.org/configuration/outbound/urltest/)

[R08] [sing-box：配置与检查](https://sing-box.sagernet.org/configuration/)

[R09] [sing-box：Deprecated](https://sing-box.sagernet.org/deprecated/)

[R10] [sing-box：Build from source](https://sing-box.sagernet.org/installation/build-from-source/)

[R12] [Mihomo：Load-Balance](https://wiki.metacubex.one/en/config/proxy-groups/load-balance/)

[R13] [Mihomo：Relay 废弃说明](https://wiki.metacubex.one/en/config/proxy-groups/relay/)

[R14] [Mihomo：官方 main.go 命令入口](https://raw.githubusercontent.com/MetaCubeX/mihomo/refs/heads/Meta/main.go)

[R15] [Shadowsocks：SIP002 URI Scheme](https://shadowsocks.org/doc/sip002.html)

[R16] [OWASP：SSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html)

[R17] [OWASP：Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)

[R18] [OWASP：Cryptographic Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html)

[R19] [PostgreSQL：SELECT 与 SKIP LOCKED](https://www.postgresql.org/docs/current/sql-select.html)

[R20] [Docker：Engine security](https://docs.docker.com/engine/security/)

[R21] [Docker Compose：secrets](https://docs.docker.com/compose/how-tos/use-secrets/)

[R22] [Xray-core：官方许可证](https://raw.githubusercontent.com/XTLS/Xray-core/main/LICENSE)

[R23] [Mihomo：官方许可证](https://raw.githubusercontent.com/MetaCubeX/mihomo/Meta/LICENSE)

[R24] [sing-box：官方许可证声明](https://raw.githubusercontent.com/SagerNet/sing-box/master/LICENSE)

[R26] [sing-box：Selector](https://sing-box.sagernet.org/configuration/outbound/selector/)

[R27] [Docker Compose：启动与健康检查顺序](https://docs.docker.com/compose/how-tos/startup-order/)

文档变更必须同步更新需求编号、设计对应章节、接口/Schema 版本和验收夹具。新增协议或内核版本不应删除旧限制记录；将“已知限制”改为“已验证”必须有可追溯证据。
