# 发布发行版

[文档目录](wiki/README.zh_CN.md) · [English](RELEASING.md)

推送以 `v` 开头的标签会触发 GitHub Actions，通过 GoReleaser 发布二进制包。[发布工作流](.github/workflows/release.yml) · [打包配置](src/.goreleaser.yaml)

## 发布内容

Linux、macOS、Windows 均构建 amd64 和 arm64 两种架构。压缩包包含 `epusdt`、`chainscanner` 和 `.env.example`。Windows 使用 ZIP，其他平台使用 tar.gz。发行页同时提供 `SHA256SUMS` 与 GitHub 构建来源证明。

## 验证并打标签

1. 确认待发布提交已进入默认分支。在 `src` 目录运行快照构建，检查 `src/dist/` 中的产物。

   ```sh
   cd src
   go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
   ```

2. 在该提交上创建附注标签并推送。以下版本号仅为示例。

   ```sh
   git tag -a v1.0.0 -m "release v1.0.0"
   git push origin v1.0.0
   ```

3. 等待 `release` 工作流完成，从发行页下载校验文件和对应平台的压缩包。验证构建来源及下载包的 SHA-256。

   ```sh
   gh attestation verify SHA256SUMS -R GMWalletApp/epusdt
   ```

## 候选版本

`v1.2.3-rc1` 一类标签将标为预发布。正式标签（如 `v1.2.3`）的更新日志与上一个稳定版本比较。
