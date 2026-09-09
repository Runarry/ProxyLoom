# T-013 受限来源抓取器

实现 `internal/safefetch`：独立 Transport、不继承环境代理、解析后 IP 校验并拨号到允许地址、TLS SNI／HTTP Host 保持原始主机。默认拒绝 HTTP。阻塞回环、私网、链路本地、多播、未指定、IPv4-mapped、CGNAT、云元数据与文档前缀。跨 origin 重定向删除 Authorization。压缩与解码体积、超时、跳转有上限。`AllowNets` 仅测试夹具使用。

验证：`go test ./internal/safefetch` 覆盖阻塞地址、userinfo 拒绝、HTTPS 钉扎与 Host／SNI、环境代理忽略、元数据重定向、跨 origin 认证头剥离、gzip／超限及错误不含 URL 或秘密。未对公网真实订阅做抓取。公开 API 仍无来源 CRUD。
