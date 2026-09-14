# T-059 自托管候选包与供应链

本轮双架构镜像构建、导出、九份 CycloneDX SBOM、锁定 Grype 扫描、内核源码归档和实际目录打包均已运行。镜像归档、清单和配置摘要见 [images.json](images.json)，扫描输入与工具身份见 [SBOM 清单](sbom-manifest.json)及[漏洞清单](vulnerability-manifest.json)。没有推送镜像、创建公开 Release 或部署生产主机。

当前扫描中，amd64/arm64 对应镜像的计数相同：API 316 项（7 Critical），Runner 428 项（22 Critical），operations 584 项（32 Critical），PostgreSQL 583 项（32 Critical）；前端构建输入为 0。计数含系统包、预编译内核及重复来源，不能视为已证实可利用的独立问题，也不能视为已清零。自有 Go 入口的可达路径分析已通过；其余适用性与升级边界见 [供应链记录](../../m3-supply-chain.md)。

打包核对同一应用源码、已验镜像、部署文件、SBOM 和扫描报告摘要，并输出 SHA256SUMS。包含三内核锁定提交的源码、143 个本机 Go/npm 构建依赖的许可材料、Go 运行时许可，以及镜像内保留的 Debian 包版权文件。补齐原包省略的上游许可时使用精确提交和独立摘要；Rolldown 同版本主包的第三方许可一并保留。

`uri-js-replace@1.0.1` 的精确上游提交只声明 MIT，未附完整版权/许可文本；包中保留原声明并在许可证清单的 remaining_review 明确记录，未自行编造版权文本。完整系统包适用性、该许可材料及 arm64 运行验收仍未闭合，因此 T-059 与 G3 保持实施中。

远端手动 CI 工作流已编写但未触发。本机分步运行有实际证据；不能把脚本存在或单独打包通过当作整个一键流水线和 G3 全部通过。
