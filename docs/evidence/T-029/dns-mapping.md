# T-029 DNS 原生映射与执行证据

执行日期：2026-09-10；最后原生执行完成于 07:34 UTC 前。工作树基线 HEAD 为 `80ad286a44c77a12c80384e70350951370be788e`，包含本窗口尚未提交的 M1 更改。本证据仅覆盖下述 DNS 编译与 Linux/amd64 执行；不等同于全部客户端导入、业务 HTTP 连通性或发布验收。

## 已实现的映射

| 语义 | Xray 26.3.27 | sing-box 1.14.0 | Mihomo 1.19.30 |
| --- | --- | --- | --- |
| local 业务 resolver | `localhost`，有下述条件规则限制 | `type: local` | `system` |
| 固定 IP UDP | 独立服务器 tag，路由到显式出口 | `type: udp`，固定 server/port 与显式 detour | `udp://IP:port#出口` |
| HTTPS 固定 IP | 保留原 URL；服务器 tag 指向显式出口 | HTTPS server/port/path/TLS；显式 detour | 原 URL 加出口 fragment；有下述 URL 限制 |
| HTTPS 域名、直连 | local bootstrap 时使用 `https+local` | `domain_resolver` 指向所选 bootstrap | `default-nameserver` 指向所选 bootstrap |
| HTTPS 域名、代理出口 | 无法保证独立 bootstrap，拒绝 | 支持 bootstrap 与 detour 同时生效 | P0 代理会转交域名给远端，拒绝 |
| 有序域名规则 | 每条规则复制目标服务器；`finalQuery: true` | 原生有序规则和 logical AND/OR | 每条规则独立 inline domain provider，按 YAML 顺序写入 `rule-set:` policy |
| 明确 final | 仅 final 的 `skipFallback` 为 false | 显式 `dns.final` | 仅一个业务 `nameserver` |

规则字段之间为 AND，同字段多个域名为 OR；suffix 同时包含自身与点分隔子域。Xray/Mihomo 将域名表达式归约为 exact/suffix 集合交集，避免大规则集的笛卡尔展开。矛盾条件为空匹配，不变成 catch-all。Mihomo 不用普通域名 policy key 合并树，避免更具体的域名覆盖前一条规则。

没有增加公共 DNS、业务 resolver 备用列表或失败直连。Xray 的直接业务出口在配套编排更改中使用 `freedom.domainStrategy: ForceIP`。sing-box 的 direct 出口需要 `domain_resolver` 才能通过锁定版本检查；配套编排在 direct 规则所在位置先执行不指定 server 的 `resolve` action，使有序 DNS 规则生效，再将已解析地址交给 direct。这一行为包含在本次原生运行中。

## Bootstrap 选择约定

节点 endpoint 为域名时，使用 DNS profile `bootstrap[0]`；这是数组顺序具有业务意义的选择约定，不是按可用性选取、轮询、并行或失败备用。HTTPS resolver 的域名独立使用自身 `bootstrap_resolver_id` 引用。固定 IP endpoint 不需要查询。其他 bootstrap 条目仍可被 HTTPS resolver 引用；不自动作为备用服务器。

sing-box 的节点 `domain_resolver` 和 HTTPS `domain_resolver` 可以分别引用相应条目。Xray 普通 socket 的可表达节点引导方式为系统 local，因此命名节点且第一 bootstrap 非 local 时返回 `/payload/bootstrap` 诊断。Mihomo 只有共享的原生 bootstrap 配置：需要解析的节点使用第一条，命名 HTTPS 直连解析器使用显式引用；如果这些选择对应不同 resolver 值，则在冲突的 HTTPS `/bootstrap_resolver_id` 返回诊断。同值的不同定义或未使用的 bootstrap 定义不构成冲突。

本证据没有独立验证命名两跳节点的引导解析；节点及两跳本身的原生执行由相关编排/链证据负责。本页运行中的 DNS 代理出口为固定 IP 的本地 SOCKS 夹具。

## 无法等价映射的精确情况

所有以下诊断均为 `COMPILE_UNMAPPED_FIELD`，包含 DNS resource ID、目标键和字段路径，不返回含秘密的 URL 内容。

| 内核 | 条件 | 字段路径 | 原因 |
| --- | --- | --- | --- |
| Xray | 命名 HTTPS 使用 UDP bootstrap，或使用非 direct 出口 | `/payload/resolvers/N/bootstrap_resolver_id` | remote DoH 没有每解析器独立 bootstrap；local DoH 绕过 dispatcher 并使用系统解析，只等价于 local + direct |
| Xray | 条件 local 规则与非 local final 共存 | `/payload/resolvers/N/kind` | 原生 localhost 自动添加私有 TLD 和无点域名优先规则，扩大条件匹配 |
| Xray | 条件 local 规则位于会匹配私有 TLD 的远端规则之前 | `/payload/rules/N/resolver_id` | 即使 final 为 local，自动优先规则仍可能抢先匹配后面的远端规则；仅 local final 不受此限制 |
| sing-box | HTTPS URL 带 query，包括空的 `?` | `/payload/resolvers/N/url` | 原生仅提供 path；拼入 query 后实际请求把 `?` 编成路径内容，改变 URL |
| Mihomo | 命名 HTTPS 使用 P0 非 direct 出口 | `/payload/resolvers/N/bootstrap_resolver_id` | TCP DNS dialer 将域名交给代理端；所选本地 bootstrap 没有被使用 |
| Mihomo | 需要不同的共享 bootstrap | `/payload/resolvers/N/bootstrap_resolver_id` | `default-nameserver`/`proxy-server-nameserver` 无法表达每个 DNS 解析器不同的 bootstrap |
| Mihomo | HTTPS URL 有 query、空 `?` 或需要 RawPath 保持原意的转义 | `/payload/resolvers/N/url` | 锁定 URL parser 重建 Scheme/Host/Path，丢失 RawQuery/RawPath；DoH GET 另写 `dns` query |

非域名 DNS 条件返回对应 `/payload/rules/N/match`；规则扩展超过 20,000 则返回 `INPUT_LIMIT_EXCEEDED`。UDP 经节点/链/策略的传输能力校验仍由编译器依赖闭包负责，本页没有宣称未实现的代理 UDP 能力。

依据为锁定版本官方源码和下述真实执行：Xray [sortClients/finalQuery](https://github.com/XTLS/Xray-core/blob/v26.3.27/app/dns/dns.go)、[local 自动域名规则与 DoH transport](https://github.com/XTLS/Xray-core/blob/v26.3.27/app/dns/nameserver.go)；sing-box [detour 与 domain_resolver 包装](https://github.com/SagerNet/sing-box/blob/v1.14.0/common/dialer/dialer.go)、[DoH path 构造](https://github.com/SagerNet/sing-box/blob/v1.14.0/dns/transport/https.go)；Mihomo [有序 policy 与 URL parser](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/config/config.go)、[首次 policy 匹配](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/dns/resolver.go)、[DNS TCP 的代理与 bootstrap 分支](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/tunnel/dns_dialer.go)、[DoH GET query 构造](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/dns/doh.go)。

## 实际验证结果

`go test ./internal/adapter/xray ./internal/adapter/singbox ./internal/adapter/mihomo`：三个包 PASS。新增单测覆盖 AND、域名边界、有序编码、矛盾规则、出口/bootstrap 字段、精确诊断及限制。`go vet` 同三个包：退出 0。六个 DNS Go 文件 `gofmt -l`：无输出。

`TestDNSNativeExecution`：12 个子测试全部 PASS，最后一次运行 1.91 秒。该测试在指定环境变量缺失时明确 SKIP；本次用下述容器命令启用，没有把常规单测中的 SKIP 计为原生成功。

| 每个锁定内核均执行 | 实际断言 |
| --- | --- |
| local-config | 最终 emitter 字节通过真实内核配置检查 |
| udp-order-and-no-fallback | 真实 SOCKS 域名拨号触发本地 UDP DNS；较早 suffix 胜过后面的 exact；suffix 自身/子域/相似字符串边界；AND 与规则集 OR；矛盾规则不命中；明确 final；NXDOMAIN 后没有查询其他业务 resolver |
| https-explicit-outbound-bootstrap | 真实本地 DoH，临时 CA 验证 TLS，观察指定 SOCKS DNS 出口；Xray/Mihomo 使用字面 IP，sing-box 使用域名且观察指定 UDP bootstrap |
| https-hostname-direct-bootstrap | 三内核均验证命名 DoH 直连；Xray 使用 local bootstrap + localhost，sing-box/Mihomo 使用指定 UDP bootstrap；无业务域名进入 bootstrap；未选 bootstrap 收到零次相关查询 |

附加 URL 断言：Xray 原样保留 `/dns%2Fquery?key=a%26b`；sing-box 原样保留 `/dns%2Fquery`。Mihomo query/RawPath 与 sing-box query 的拒绝由单测覆盖。

曾经复现而后修正的失败：初始 DoH 夹具只支持 HTTP/1.1/POST，修正为支持 HTTP/2 与 GET 后继续测试；Mihomo 命名 DoH 走 SOCKS 时夹具捕获未引导的域名，已变为精确诊断；sing-box query 被发成 `/dns/query%3Fkey=a&b`，已变为精确诊断。以上历史失败未记为最终通过。

所有运行服务都位于容器 loopback，容器使用 `--network none`。业务检查只读取 TCP 夹具标记 `dns-ok`；没有访问真实节点、公网 DNS 或业务 HTTP。TLS 夹具证书只存在临时目录，未关闭证书验证。arm64 和真实客户端导入未执行。

## 复现命令

在仓库根目录的 PowerShell 中：

```powershell
$env:GOCACHE='G:\Projects\ProxyLoom\.cache\go-build-m1-dns'
go test ./internal/adapter/xray ./internal/adapter/singbox ./internal/adapter/mihomo
go vet ./internal/adapter/xray ./internal/adapter/singbox ./internal/adapter/mihomo
$env:GOOS='linux'
$env:GOARCH='amd64'
$env:CGO_ENABLED='0'
go test -c -o .cache/m1-dns-native.test ./internal/adapter/mihomo
$dnsRuntimeImage=(Get-Content deploy/tools.lock.json | ConvertFrom-Json).images.runtime
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true --user 10002:10002 --tmpfs /work:uid=10002,mode=0700,exec --mount type=bind,source=G:/Projects/ProxyLoom/.cache,target=/cache,readonly --env PROXYLOOM_DNS_NATIVE_CORES=/work/cores --env TMPDIR=/work $dnsRuntimeImage /bin/sh -c 'mkdir /work/cores; cp /cache/cores/xray/v26.3.27/amd64/xray /cache/cores/sing-box/v1.14.0/amd64/sing-box /cache/cores/mihomo/v1.19.30/amd64/mihomo /work/cores/; cp /cache/m1-dns-native.test /work/dns.test; chmod 0755 /work/cores/* /work/dns.test; /work/dns.test -test.run TestDNSNativeExecution -test.v -test.timeout 100s'
```

Docker 按用户约定在 sandbox 外执行。运行镜像读取已有 digest lock；每个测试开始前校验内核二进制 SHA256 必须与 `compat/cores.lock.yaml` 一致。

最终测试源码与二进制 SHA256（原生运行使用的测试二进制包含配套编排更改）：

```text
internal/adapter/xray/dns.go 90f5ed350c0335b2854b5912322510c997036bc529d1b1c9aa048ffd4d3b7f90
internal/adapter/xray/dns_test.go b7dbcc3c66b9ffbc9f8e5dadc5850f1f429a97c668475dba84898ffc7c260667
internal/adapter/singbox/dns.go 9068ca774822e62bd5054c985863a96d779f8c58b6a9c0b3d3e8952f90b9528c
internal/adapter/singbox/dns_test.go c18efbe283a6d0ab2c88c81be535a2ebffb94e910e8b908568ca20f6fbed0697
internal/adapter/mihomo/dns.go 65b5ea3e8a6b19bbf4ae150b848b3c59be6c6a7e662185c9a9f4ba64ce55d744
internal/adapter/mihomo/dns_test.go 3cc213c88981e8627ec7c80bc745efcbac3b53886fa280dc84d5b765c25330bf
.cache/m1-dns-native.test ae1a65f551e49b09b0b00f230132dba590e23862d6545db6996df15632f445e9
```
