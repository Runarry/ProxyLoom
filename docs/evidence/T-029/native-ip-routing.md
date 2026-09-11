# T-029 IP 路由解析模式原生验收

2026-09-10，用户明确批准“接受 sing-box 此项限制并记录范围变更”。本记录保留已证实的内核限制，不把不支持的组合标记为原生通过；G1 按已批准的目标范围验收。

## 生效范围

| 目标与模式 | 结果 |
| --- | --- |
| 三内核 `preserve_domain` | 支持；域名目标不为 IP 规则主动查询 DNS，字面 IP 仍参与 CIDR 匹配 |
| Xray / Mihomo `resolve_for_ip_rules` | 支持；使用明确的 DNS 方案，匹配有序规则与显式 final |
| sing-box `resolve_for_ip_rules` 且有生效 IP 条件 | 编译拒绝；包括引用规则集展开后的 CIDR 条件 |
| sing-box `resolve_for_ip_rules` 无生效 IP 条件 | 支持；不插入全局预解析。禁用 IP 规则、纯域名规则集不触发拒绝 |

拒绝返回 `COMPILE_UNMAPPED_FIELD`，包含路由资源 ID、目标键和 `/payload/domain_resolution_mode`；不产生配置字节。direct 动作仍在自己的路由位置使用明确的 DNS 方案。没有更换内核锁、修改 Xray/Mihomo 行为或自动改变用户选择的模式。

## 反例与依据

最小反例为：先检查 IP-CIDR 规则，再检查能够匹配目标域名的代理规则；明确配置的本地 UDP DNS 对该目标返回 NXDOMAIN。Xray 与 Mihomo 均继续匹配后面的域名规则并通过代理完成请求。sing-box v1.14.0 在 `resolve` 失败时结束连接，后面的域名规则没有机会匹配。

原先在所有路由规则前插入 `resolve` 还会使首条域名规则和完全没有 IP 条件的代理 final 被 DNS 失败抢先中断。只把它移动到首个 IP 规则之前仍不能解决上述反例。额外验证了 IP 规则中域名或端口 AND 条件不匹配的情形：Xray/Mihomo 的内部 DNS 查询顺序不同，但都能继续成功匹配后续域名规则，因此验收比较路由结果，不要求这些内部条件具有完全相同的查询次数。

锁定版本的 [matchRule/actionResolve](https://github.com/SagerNet/sing-box/blob/v1.14.0/route/route.go#L654) 在 DNS lookup 错误后返回；[RouteActionResolve 可用字段](https://github.com/SagerNet/sing-box/blob/v1.14.0/option/rule_action.go#L309) 没有忽略错误或继续路由的选项。[IPCIDRItem.Match](https://github.com/SagerNet/sing-box/blob/v1.14.0/route/rule/rule_item_cidr.go#L73) 只检查现有目标地址，不提供另一个按需 DNS 路由机制。这些源码和原生结果支持对当前组合返回兼容诊断。

`TestM1NativeSingBoxIPDNSFailureLimitation` 保留受控原生反例：先验证编译器生成的 preserve-domain 对照配置，再只在测试中插入原生 `resolve` 动作。后者明确标记为非编译器产物，并验证 DNS 有查询、请求失败且两个代理计数均为零。完整结果与配置/内核摘要见 [native-ip-routing-limitation.json](native-ip-routing-limitation.json)；该反例成功复现不表示该配置获得支持。

## 验证

测试定义位于 `internal/compiler/m1_ip_routing_test.go`。支持范围的原生矩阵为五个“内核 × 模式”组合，每个执行 12 个请求，覆盖早期/后续域名首匹配、DNS 失败后的继续匹配、CIDR 内 OR、域名/端口/网络 AND、字面 IP、显式代理 final 和 reject。三个内核另执行无 IP 规则的 NXDOMAIN 代理 final 对照。sing-box 不支持的组合验证编译诊断与零产物。

编译守卫另覆盖直接 CIDR、域名与 CIDR 混合规则集、禁用 IP 规则、preserve-domain、纯域名规则和纯域名规则集。运行命令：

```text
go test -mod=readonly ./internal/compiler ./internal/adapter/singbox
go vet -mod=readonly ./internal/compiler ./internal/adapter/singbox
node scripts/verify-m1-native.mjs
```

三项均已通过，原生脚本确认运行前后的源码摘要一致。原生脚本使用锁定的 Linux/amd64 三内核和带摘要的 Debian runtime 镜像，Docker 设置 `--network none`、只读文件系统、移除 capabilities 和资源上限；DNS、目标与代理全部在容器回环地址。完整原生运行同时复验原有 72 份配置、策略选择、轮询、时延策略、AND/OR/顺序、链引导及失败关闭行为。新增验证包括 60 个支持模式路由请求、三个无 IP 规则请求、编译拒绝断言及两个原生限制对照请求，均符合各自预期。

[native-compiler-output.txt](native-compiler-output.txt)、[native-compiler-source.json](native-compiler-source.json)、[native-ip-routing.json](native-ip-routing.json) 和上述限制 JSON 记录实际结果与源码摘要。原生反例的本次实际结果：preserve-domain 对照 DNS 查询 0 次、代理 A 收到 1 次请求并成功；插入 resolve 后 DNS 查询 2 次、代理 A/B 均收到 0 次请求且请求失败。所有新增夹具无真实节点凭证。
