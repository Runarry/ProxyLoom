# M3 自托管供应链与补丁记录

交付入口为 `node scripts/prepare-self-hosted.mjs`，可在本机或手动触发的 `self-hosted.yml` 工作流运行。入口依次执行锁定内核准备、质量与兼容回归、双架构构建、amd64 隔离部署和容量验收、SBOM、漏洞扫描、源码与许可证汇总及摘要打包。它不推送镜像、不创建公开 Release、不访问生产主机；arm64 构建成功继续标记运行未验。当前工作流仅编写，未在远端触发。

可单独运行构建、镜像导出、部署验收、SBOM 和打包步骤以继续已有验收。打包检查应用源、已验镜像、部署脚本、SBOM 与扫描报告的摘要关联。不能用旧镜像的通过报告证明新镜像通过。三个内核的官方归档和二进制分别验摘要，源码归档绑定已锁定的提交。

`stage-release-notices.mjs` 按 `deploy/notices.lock.json` 获取原依赖归档遗漏的许可材料，固定上游提交、文件和内容摘要。包内保留第三方声明文件，不只匹配名为 LICENSE 的文件。`uri-js-replace@1.0.1` 上游只提供 MIT 声明，完整文本缺失明确列在许可清单的 remaining_review 中；不擅自补写版权声明。

扫描工具锁定 Syft 1.51.1 和 Grype 0.118.0 的版本、官方发布归档及可执行文件摘要。Grype 在本机使用公开漏洞数据库比对 SBOM，保存数据库版本、生成时间及内容来源；不上传节点、凭证或项目源码。报告的 `scan_result=pass` 只表示扫描完成，`disposition=review_required` 表示条目尚需结合实际路径判断。

首轮候选镜像扫描发现 Go 1.26.0、pgx 5.7.6、间接 QUIC 依赖、旧 PostgreSQL 基础镜像及 js-yaml 4.3.1 的告警。按当前实际代码路径，实施同主版本补丁：Go 1.26.8、pgx 5.9.2、x/crypto 0.56.0、quic-go 0.59.1、PostgreSQL 17.11，以及仅构建期的 js-yaml 4.3.2。直接依赖、传递依赖完整性、Docker 摘要和 CI 工具版本同步锁定。三内核的 CoreBuild ID 和原始摘要不修改。

补丁依据：[Go 1.26 维护记录](https://go.dev/doc/devel/release#go1.26.8)、[pgx 漏洞记录](https://pkg.go.dev/vuln/GO-2026-4771)、[js-yaml 官方补丁](https://github.com/nodeca/js-yaml/releases/tag/4.3.2)、[PostgreSQL 17 维护记录](https://www.postgresql.org/docs/17/release.html)。Go 官方 govulncheck v1.8.0 使用 2026-09-10 数据库，对 Linux/amd64 的 server、runner、operations 入口执行符号路径分析；补丁后的报告未发现可达条目。该分析不覆盖预编译内核或系统动态库，也不是未来漏洞的保证。

最终包必须保留扫描原始结果、适用性判断与未解决项。不能仅因隔离、非 root 或可信管理员，就把所有内核／系统包告警标为已修复；不能以扫描工具返回成功替代 G3 安全结论。

## 剩余告警的范围判断

截至 2026-09-14，已锁定内核仍携带较旧 Go 模块；本次不在相同 CoreBuild ID 下替换二进制。Runner 在线配置只开放回环 SOCKS 入口，P0 类型模型不接受 SSH 节点。由此推断，SSH known_hosts 的 CA 撤销缺陷（[GO-2026-5021](https://pkg.go.dev/vuln/GO-2026-5021)）以及需要 gRPC 服务端路径授权拦截器的 [CVE-2026-33186](https://github.com/advisories/GHSA-p77j-4mvh-x3m3)，不属于当前在线测试入口的执行路径。该判断仅针对相应条件，不代表整个内核无漏洞，也不对导出配置在其他客户端环境的行为作保证。

镜像中的 glibc、Perl、OpenSSL、libxml2、SQLite 等告警保留原始等级和修复状态。例如 [Debian 对 CVE-2026-5450 的记录](https://security-tracker.debian.org/tracker/CVE-2026-5450)仍将 bookworm 标为受影响，触发需要特定 scanf 格式宽度；发行版将其列为较小问题。扫描等级与发行版结论并不等价，不能因此删除记录。API/Runner 自有程序采用 CGO_ENABLED=0，但 PostgreSQL 和运维工具包含动态库路径；这些镜像条目的全面适用性复核仍未完成。

候选包因此保持 `G3_open`，不声称“镜像漏洞清零”。后续补丁需形成新镜像摘要；如更新内核，还须登记新 CoreBuild 并重新执行适用兼容验收。
