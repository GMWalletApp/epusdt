# Epusdt — Easy Payment USDT

<p align="center">
  <img src="https://gmwallet.app/favicon.png" alt="Epusdt Logo - Multi-chain Crypto Payment Gateway" width="120">
</p>

<p align="center">
  <strong>开源多链多币种 Crypto 支付网关 · 实际采用率 Top 1</strong>
</p>

<p align="center">
  <a href="./README.en.md">English</a> |
  <a href="./README.md">简体中文</a>
</p>

<p align="center">
  <a href="https://epusdt.com"><img src="https://img.shields.io/badge/官网文档-epusdt.com-blue?style=for-the-badge" alt="Official Docs"></a>
  <a href="https://t.me/epusdt"><img src="https://img.shields.io/badge/Telegram-频道-26A5E4?style=for-the-badge&logo=telegram&logoColor=white" alt="Telegram Channel"></a>
  <a href="https://t.me/epusdt_group"><img src="https://img.shields.io/badge/Telegram-交流群-26A5E4?style=for-the-badge&logo=telegram&logoColor=white" alt="Telegram Group"></a>
</p>

<p align="center">
  <a href="https://github.com/GMWalletApp/epusdt/stargazers"><img src="https://img.shields.io/github/stars/GMWalletApp/epusdt?style=flat-square&color=f5c542" alt="GitHub Stars 3000+"></a>
  <a href="https://www.gnu.org/licenses/gpl-3.0.html"><img src="https://img.shields.io/badge/License-GPLv3-blue?style=flat-square" alt="GPLv3 License"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.16+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.16+"></a>
  <a href="https://github.com/GMWalletApp/epusdt/releases"><img src="https://img.shields.io/github/v/release/GMWalletApp/epusdt?style=flat-square&color=green" alt="Latest Release"></a>
</p>

---

## What is Epusdt?

**Epusdt** (Easy Payment USDT) 是一个基于 Go 构建、支持私有化部署的 **多链多币种 Crypto 支付网关**。它从最初的 TRC20 单链方案逐步演进为完整的 **多链收款平台**，让任意网站或应用都能快速接入多条链、多种代币的加密支付能力。没有第三方托管，没有平台抽成，资金直接进入你的钱包。

> **GitHub Star 3000+** · **已支持站点解决方案 10+** · **Crypto 支付工具实际采用率 Top 1**

私有部署，按 HTTP API 接入，几分钟内就可以开始接收 **Crypto Payments**。

### 默认内置网络与代币

| 网络 | 代币 |
|------|------|
| **TRC20** (Tron) | USDT、TRX |
| **ERC20** (Ethereum) | USDT、USDC |
| **Solana** | USDT、USDC、SOL |
| **BEP20** (BSC) | USDT、USDC |
| **Polygon** | USDT、USDC、USDC.e |
| **Plasma** | USDT |
| **Base** (Chain ID 8453) | USDC |
| **Arbitrum One** (Chain ID 42161) | USDC、USDT（官方合约已升级为 USDT0） |
| **TON** | TON、USDT |
| **Aptos** | USDC、USDT |
| **更多** | 持续扩展中… |

> Base 默认使用 Circle 原生 USDC，不包含 USDbC；Arbitrum One 默认使用 Circle 原生 USDC 和官方 USDT/USDT0 合约，暂不支持两条链的原生 ETH。实际可用资产还取决于后台是否启用对应链、代币，以及是否配置了该链钱包地址和可用 RPC 节点，可通过 `GET /payments/gmpay/v1/config` 查询。

### 默认监控合约与资产标识

以下地址是新数据库首次启动时写入的默认配置。EVM/TRON 使用代币合约地址，Solana 使用 Mint 地址，TON 使用 Jetton Master 地址，Aptos 使用 Fungible Asset Metadata 地址；原生资产没有合约地址。已有数据库中的同网络、同代币配置不会被启动过程覆盖，运行时应以管理后台和数据库中的 `chain_tokens` 实际记录为准。

| 网络 | `network` 参数 | 代币 | 合约或资产标识 | 精度 |
|------|-----------------|------|------------------|------|
| TRON | `tron` | USDT | `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 6 |
| TRON | `tron` | TRX | 原生资产，无合约地址 | 6 |
| Ethereum | `ethereum` | USDT | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 6 |
| Ethereum | `ethereum` | USDC | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | 6 |
| Solana | `solana` | USDT | `Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB` | 6 |
| Solana | `solana` | USDC | `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v` | 6 |
| Solana | `solana` | SOL | 原生资产，无合约地址 | 9 |
| BSC | `binance` | USDT | `0x55d398326f99059fF775485246999027B3197955` | 18 |
| BSC | `binance` | USDC | `0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d` | 18 |
| Polygon | `polygon` | USDT | `0xc2132D05D31c914a87C6611C10748AEb04B58e8F` | 6 |
| Polygon | `polygon` | USDC | `0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359` | 6 |
| Polygon | `polygon` | USDC.e | `0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174` | 6 |
| Plasma | `plasma` | USDT | `0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb` | 6 |
| Base | `base` | USDC | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` | 6 |
| Arbitrum One | `arbitrum` | USDC | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` | 6 |
| Arbitrum One | `arbitrum` | USDT | `0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9` | 6 |
| TON | `ton` | TON | 原生资产，无合约地址 | 9 |
| TON | `ton` | USDT | `0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe` | 6 |
| Aptos | `aptos` | USDC | `0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b` | 6 |
| Aptos | `aptos` | USDT | `0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b` | 6 |

---

## 安全审计
Epusdt 已完成第三方安全审计。
[查看安全审计报告](https://github.com/VectorBits/audit/blob/main/epusdt-secure-audit-report-2026-05-14.pdf)

---

## 广泛兼容，即插即用

无论你运营的是哪类系统，Epusdt 均可基于现有接口方案，**无需重构业务逻辑**，快速接入，立即获得 Crypto 收款能力，低成本扩展全球支付场景：

| 领域 | 已支持系统 |
|------|-----------|
| **AI 分发** | [Sub2API](https://github.com/Wei-Shaw/sub2api)、[NewAPI](https://github.com/QuantumNous/new-api) |
| **发卡系统** | [独角数卡（Dujiaoka）](https://dujiao-next.com/)、[异次元发卡](https://github.com/lizhipay/acg-faka) |
| **代理面板** | [V2Board](https://github.com/v2board/v2board)、[XBoard](https://github.com/cedar2025/Xboard)、[xiaoV2board](https://github.com/wyx2685/v2board/)、[SSPanel](https://github.com/anankke/sspanel-uim) |
| **建站生态** | [WordPress](https://wordpress.com/)、[WHMCS](https://www.whmcs.com/) |
| **Epay 兼容** | 兼容各类支持 Epay 易支付接口的平台 |
| **更多** | 简易 HTTP API，10 分钟内接入 |

---

## 核心特性

- **多链多币种** — 支持 TRON、Ethereum、Solana、BSC、Polygon、Plasma、Base、Arbitrum One、TON、Aptos 等网络
- **私有化部署** — 资金完全自主掌控
- **零依赖运行** — 单个二进制即可启动，低并发场景无需 MySQL + Redis
- **跨平台** — 支持 x86 / ARM 架构的 Windows / Linux / Mac
- **多钱包轮询** — 自动轮换收款地址，提高并发处理能力
- **异步队列** — 高性能消息回调，适配高并发场景
- **HTTP API** — 标准化接口，任何语言 / 框架都能快速集成
- **Telegram Bot** — 实时支付通知，快捷管理与监控

---

## 文档与教程

完整文档请访问：**[epusdt.com](https://epusdt.com)**

快速入门：

| 教程 | 说明 |
|------|------|
| [仓库内：epctl 安装脚本](wiki/EPCTL.md) | Linux 二进制安装、升级、状态查看与 Docker 验收脚本 |
| [Docker 部署](https://epusdt.com/guide/installation/docker) | 推荐方式，一键启动 |
| [宝塔面板部署](https://epusdt.com/guide/installation/aapanel) | 适合宝塔用户 |
| [手动部署](https://epusdt.com/guide/installation/manual.html) | 完全手动控制 |
| [开发者 API 文档](https://epusdt.com/zh/guide/integration/gmpay.html) | 接口集成指南 |
| [仓库内：完整 API 文档](wiki/API.md) | 当前代码路由、签名、请求参数、回调与示例 |

仓库内还提供顶层脚本：

- [`./build.sh`](./build.sh) 用于一键编译当前平台、指定平台或全部平台，产物输出到 `dist/`
- [`./epctl`](./epctl) 用于 Linux 二进制安装、升级、查看配置、状态和初始化密码
- [`./epctl-docker-test.sh`](./epctl-docker-test.sh) 用于在本机 Docker 里跑 Ubuntu + systemd 的真实安装验收

一键编译当前平台：

```bash
./build.sh
```

编译 Linux AMD64 或全部支持平台：

```bash
./build.sh linux-amd64
./build.sh all
```

脚本会自动写入版本号、Git 提交号和编译时间，并生成压缩包及 SHA-256 校验文件。可通过 `BUILD_VERSION=v1.2.3 ./build.sh linux-amd64` 指定版本号。

---

## API 暴露与认证边界

正常运行时，HTTP 端口同时承载收银台、商户支付接口和管理后台接口。部署时应通过 HTTPS 反向代理对外提供服务，并根据下表限制不需要公开的路径。

### 公开及订单访问接口

| 方法 | 路径 | 认证方式 | 用途 |
|------|------|----------|------|
| `POST` | `/` | 无 | 服务探测 |
| `GET` | `/payments/gmpay/v1/config` | 无 | 获取公开站点配置及当前可用资产 |
| `GET` | `/pay/checkout-counter/{trade_id}` | 无 | 跳转到收银台页面 |
| `GET` | `/pay/checkout-counter-resp/{trade_id}` | 无 | 获取收银台订单数据 |
| `GET` | `/pay/check-status/{trade_id}` | 无 | 查询订单状态 |
| `GET` | `/pay/return/{trade_id}` | 无 | EPay 支付完成后的商户跳转 |
| `POST` | `/pay/submit-tx-hash/{trade_id}` | `trade_id` 能力凭证 | 用户提交链上交易哈希进行补单验证 |
| `POST` | `/pay/switch-network` | `trade_id` 能力凭证 | 为订单选择或切换支付网络/通道 |

`trade_id` 可用于读取订单状态、切换支付目标或提交交易哈希，应当视为不可公开传播的能力凭证，不要写入公开日志、统计参数或第三方页面。

### 商户及支付平台接口

| 方法 | 路径 | 认证方式 |
|------|------|----------|
| `POST` | `/payments/gmpay/v1/order/create-transaction` | 商户 PID、API Key 签名及可选 IP 白名单 |
| `GET/POST` | `/payments/epay/v1/order/create-transaction/submit.php` | EPay 签名及可选 IP 白名单 |
| `POST` | `/payments/okpay/v1/notify` | OkPay 平台签名 |

Base 与 Arbitrum One 复用上述通用接口，不提供单独的链专用 API：

| 网络 | GMPay 参数 | EPay `type` 示例 |
|------|------------|------------------|
| Base | `network=base`、`token=USDC` | `USDC.base` |
| Arbitrum One | `network=arbitrum`、`token=USDC` | `USDC.arbitrum` |
| Arbitrum One | `network=arbitrum`、`token=USDT` | `USDT.arbitrum` |

### 管理后台接口

- `POST /admin/api/v1/auth/login` 和 `GET /admin/api/v1/auth/init-password-hash` 不要求 JWT。
- 其余 `/admin/api/v1/*` 接口均要求管理员 JWT，覆盖 API Key、通知渠道、链与代币、RPC、钱包、订单、仪表盘和系统设置管理。
- `GET /admin/api/v1/dashboard/rpc-stats` 是需要 JWT 的 SSE 长连接接口。

### 首次安装接口

当 `.env` 不存在或配置了 `install=true` 时，程序会先开放以下安装接口，完成安装后才启动正常业务 API：

| 方法 | 路径 | 认证方式 |
|------|------|----------|
| `GET` | `/api/install/defaults` | 无 |
| `POST` | `/api/install` | 无 |

安装服务默认监听 `:8000`，`POST /api/install` 会初始化数据库并返回初始管理员密码。首次启动必须限制在本机或可信内网完成，不要在未安装状态下直接将 `8000` 端口暴露到公网。

完整字段、签名算法、响应结构和回调示例请查看 [仓库内 API 文档](wiki/API.md)。

---

## 项目结构

```text
Epusdt
├── epctl       Linux 二进制安装与运维脚本
├── src/        项目核心代码
└── wiki/       文档与知识库
```

---

## 程序截图

<table>
  <tr>
    <td align="center" valign="top">
      <img src="wiki/img/web2.png" alt="Epusdt 管理面板首页" height="260"><br>
      <sub>管理面板首页</sub>
    </td>
    <td align="center" valign="top">
      <img src="wiki/img/web1.png" alt="Epusdt 管理面板" height="260"><br>
      <sub>管理面板</sub>
    </td>
    <td align="center" valign="top">
      <img src="wiki/img/pay1.jpeg" alt="Epusdt 收银台" height="260"><br>
      <sub>收银台</sub>
    </td>
    <td align="center" valign="top">
      <img src="wiki/img/pay2.jpeg" alt="Epusdt 支付页面" height="260"><br>
      <sub>支付页面</sub>
    </td>
  </tr>
</table>

---

## 实现原理

Epusdt 通过监听多条区块链网络（TRON、Ethereum、BSC、Polygon、Base、Arbitrum One、Solana、TON、Aptos 等）的 API 或 RPC 节点，实时捕获钱包地址的代币入账事件，利用**金额差异**与**时效性**精确匹配交易归属：

```text
工作流程：
1. 客户发起支付，需支付 20.05 USDT
2. 系统在哈希表中查找可用的钱包地址 + 金额组合
3. 若 address_1:20.05 未被占用 -> 锁定该组合（有效期 10 分钟），返回给客户
4. 若已被占用 -> 自动累加 0.0001 尝试下一个金额组合（最多 100 次）
5. 后台线程持续监听所有钱包的入账事件，金额匹配则确认支付成功
```

![Epusdt 支付流程图](wiki/img/implementation_principle.jpg)

---

## 社区与支持

**遇到问题？** 请优先在 GitHub 提交 [Issue](https://github.com/GMWalletApp/epusdt/issues)，我们会优先处理反馈。

| 渠道 | 链接 |
|------|------|
| Epusdt 频道 | [https://t.me/epusdt](https://t.me/epusdt) |
| Epusdt 交流群 | [https://t.me/epusdt_group](https://t.me/epusdt_group) |
| 官方文档站 | [https://epusdt.com](https://epusdt.com) |

---

## Star History

<a href="https://www.star-history.com/?type=date&repos=gmwalletapp%2Fepusdt">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
 </picture>
</a>

---

## 开源协议

Epusdt 遵守 [GPLv3](https://www.gnu.org/licenses/gpl-3.0.html) 开源协议。

---

## 免责声明及使用条款

EPusdt 由 Good Morning Technology, LLC 以免费、开源、非盈利及非托管性质开发和披露，仅供学习、研究与技术交流使用。项目本身不构成投资、金融、法律、税务、合规或任何其他专业建议，也不应被视为对任何资产、交易结果、收益、资金安全、技术可用性或特定用途作出任何保证。

Good Morning Technology, LLC 为依据美国法律设立的主体，并将在适用法律法规范围内履行相应合规义务。就美国监管框架而言，虚拟货币、数字资产及相关技术服务是否构成 money transmission、money services business（MSB）或其他受监管活动，通常取决于具体业务模式，包括项目方是否接收、持有、控制或传输资金或价值，是否代表用户托管资产，以及是否从事兑换、支付、清算、结算或其他中介性金融服务。

作为免费、开源、非盈利、非托管的软件项目，EPusdt 的代码披露、文档说明、技术交流及相关开发活动，本身不应被理解为 Good Morning Technology, LLC 或其贡献者对任何用户后续使用、修改、部署、集成、分发或二次开发行为的授权、背书、控制、参与、保证或承诺。

用户对本项目的实际使用方式、使用目的及后续行为由其自行决定，Good Morning Technology, LLC 及其贡献者无法控制、审查或限制该等行为。用户应自行确保其使用、修改、部署、集成、分发或二次开发行为符合所在地适用法律法规、监管要求、制裁规则及第三方权利，并独立承担由此产生的全部风险、责任与后果。

加密资产属于高风险新兴资产类别，包括稳定币在内的数字资产均可能发生剧烈波动、脱锚、流动性不足、技术故障、监管变化或价值归零等风险。本项目所有代码、文档及相关材料均按“现状”和“可用状态”提供。除适用法律另有强制规定外，Good Morning Technology, LLC 及其贡献者不因用户使用、无法使用、错误使用、违法使用、修改、部署、集成、分发、二次开发或依赖本项目而产生的任何直接或间接损失承担责任。

---

<p align="center">
  <sub>
    <b>Keywords:</b> USDT Payment Gateway · Crypto Payment · Multi-chain Payment · TRC20 Payment · ERC20 Payment · BEP20 Payment ·
    Self-hosted Crypto Gateway · OneAPI Payment · NewAPI Payment · 独角数卡支付 · 异次元发卡支付方式 ·
    V2Board Payment · XBoard Payment · SSPanel 支付接口 ·
    WordPress Crypto Payment · WHMCS USDT Payment · Polygon USDT ·
    Epusdt · Easy Payment USDT · Open Source Payment Gateway · 多链收款
  </sub>
</p>
