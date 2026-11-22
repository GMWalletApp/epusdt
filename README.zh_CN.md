<div align="center">

<img src="./src/www/images/logo.png" alt="EPUSDT" width="100">

# EPUSDT

**面向商户的自托管加密支付网关**

<p><a href="./README.md">English</a> · 简体中文 · <a href="https://epusdt.com/">其他语言请访问 EPUSDT 官网</a></p>

<p>
  <a href="https://epusdt.com/zh/api/payment">商户 API</a> ·
  <a href="https://epusdt.com/zh/guide/intro">文档目录</a> ·
  <a href="https://t.me/epusdt">Telegram 频道</a> ·
  <a href="https://t.me/epusdt_group">Telegram 交流群</a>
</p>

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GM_Wallet_Community_2.2-blue" alt="GM Wallet Community License 2.2"></a>
  <a href="https://github.com/GMWalletApp/epusdt/releases/latest"><img src="https://img.shields.io/github/v/release/GMWalletApp/epusdt?color=brightgreen" alt="GitHub release"></a>
  <a href="https://hub.docker.com/r/gmwallet/epusdt"><img src="https://img.shields.io/badge/Docker-gmwallet%2Fepusdt-2496ED?logo=docker&logoColor=white" alt="Docker Hub"></a>
</p>

</div>

## 开发

EPUSDT 使用 Go 和 SQLite。网关通过 GMPay 或易支付接口创建订单；内置 ChainScanner 跟踪收款地址，网关核验到账后向商户发送签名回调。

使用 Go 1.25 或更高版本，在仓库根目录执行：

```sh
cd src
go test ./...
make build
```

二进制文件为 `src/bin/epusdt`。

| 目录 | 用途 |
| --- | --- |
| [src](src/) | HTTP 路由、订单状态、支付核验和回调 |
| [chainscanner](chainscanner/) | 固定版本的链上扫描器；通过 [同步脚本](scripts/sync-chainscanner) 更新 |
| [sdk/python](sdk/python/) | 签名及创建订单的 Python 示例 |
| [src/docs](src/docs/) | 生成的 Swagger 规范和 API 错误码 |

[开发规范](DEVELOPMENT_STANDARD.zh_CN.md) · [代码审查](REVIEW.zh_CN.md) · [发布流程](RELEASING.zh_CN.md)

## 使用文档

[支持的网络、币种和完整合约地址](https://epusdt.com/zh/guide/intro#networks) · [Docker 部署与升级](https://epusdt.com/zh/guide/installation/docker) · [商户 API](https://epusdt.com/zh/api/payment) · [易支付接入](https://epusdt.com/zh/guide/integration/epay)

A7A5 支持以下主网，需要商户按网络启用。

| 网络 | A7A5 合约地址 | 小数位 |
| --- | --- | ---: |
| TRON (TRC-20) | `TLeVfrdym8RoJreJ23dAGyfJDygRtiWKBZ` | 6 |
| Ethereum (ERC-20) | `0x6fA0BE17e4beA2fCfA22ef89BF8ac9aab0AB0fc9` | 6 |

## 部署或迁移到 EPUSDT

点击提示词右上角的复制按钮，粘贴到 Codex、Claude Code、Cursor、OpenCode等Agent 的对话框中。

```text
读取 https://epusdt.com/agent-setup/prompt.md。先检查我的目标环境，按现状用 Docker 新装、升级或从 bepusdt、TokenPay 迁移到 EPUSDT（GMPay），保全原有数据。新实例请打开初始化页面，由我自己完成密码设置。
```

[查看完整部署清单](https://epusdt.com/agent-setup/prompt.md)

## 安全审计与许可

[VectorBits 安全审计报告（2026 年 5 月）](https://github.com/VectorBits/audit/blob/main/epusdt-secure-audit-report-2026-05-14.pdf) · [GM Wallet 社区许可 2.2](LICENSE)

公司及其关联实体过去 12 个月的全球合计收入严格超过 100 万美元，即使亏损也须购买许可。按社区条款，修改版在发布、交付或上线运行时须免费向公众提供该版本的完整对应源码，显著保留指定署名及原项目链接。每个修改版本（包括 SaaS 上线版本）还须按第 13 节通过 PR 或分支将该版本的实际修改提交给原项目，并注明提交号。GM Wallet 自行决定是否合并。另行签署的商业合同可约定不同条款。 [许可](LICENSE) · [贡献者条款](CONTRIBUTING.md) · [info@gmwallet.app](mailto:info@gmwallet.app)

<details>
<summary>免责声明及使用条款</summary>

EPusdt 由 Good Morning Technology, LLC 开发和发布，是一款按项目许可提供源代码的非托管软件。项目本身不构成投资、金融、法律、税务、合规或任何其他专业建议，也不应被视为对任何资产、交易结果、收益、资金安全、技术可用性或特定用途作出任何保证。

Good Morning Technology, LLC 为依据美国法律设立的主体，并将在适用法律法规范围内履行相应合规义务。就美国监管框架而言，虚拟货币、数字资产及相关技术服务是否构成 money transmission、money services business（MSB）或其他受监管活动，通常取决于具体业务模式，包括项目方是否接收、持有、控制或传输资金或价值，是否代表用户托管资产，以及是否从事兑换、支付、清算、结算或其他中介性金融服务。

EPusdt 的代码披露、文档说明、技术交流及相关开发活动，本身不应被理解为 Good Morning Technology, LLC 或其贡献者对任何用户后续使用、修改、部署、集成、分发或二次开发行为的背书、控制、参与、保证或承诺。

用户对本项目的实际使用方式、使用目的及后续行为由其自行决定，Good Morning Technology, LLC 及其贡献者无法控制、审查或限制该等行为。用户应自行确保其使用、修改、部署、集成、分发或二次开发行为符合所在地适用法律法规、监管要求、制裁规则及第三方权利，并独立承担由此产生的全部风险、责任与后果。

加密资产属于高风险新兴资产类别，包括稳定币在内的数字资产均可能发生剧烈波动、脱锚、流动性不足、技术故障、监管变化或价值归零等风险。本项目所有代码、文档及相关材料均按“现状”和“可用状态”提供。除适用法律另有强制规定外，Good Morning Technology, LLC 及其贡献者不因用户使用、无法使用、错误使用、违法使用、修改、部署、集成、分发、二次开发或依赖本项目而产生的任何直接或间接损失承担责任。

</details>
