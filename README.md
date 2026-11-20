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

### 已支持网络与代币

| 网络 | 代币 |
|------|------|
| **TRC20** (Tron) | USDT、TRX |
| **ERC20** (Ethereum) | USDT、USDC、ETH |
| **Solana** | USDT、USDC |
| **BEP20** (BSC) | USDT、USDC、BNB |
| **Polygon** | USDT、USDC |
| **Aptos** | USDC、USDT |
| **更多** | 持续扩展中… |

> 具体支持的链与代币以 [最新版本](https://github.com/GMWalletApp/epusdt/releases) 及 [官方文档](https://epusdt.com) 为准。

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

- **多链多币种** — 支持 TRC20、ERC20、BEP20、Polygon、Aptos 等主流网络
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

仓库内还提供顶层脚本：

- [`./epctl`](./epctl) 用于 Linux 二进制安装、升级、查看配置、状态和初始化密码
- [`./epctl-docker-test.sh`](./epctl-docker-test.sh) 用于在本机 Docker 里跑 Ubuntu + systemd 的真实安装验收

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

Epusdt 通过监听多条区块链网络（TRC20、ERC20、BEP20、Polygon 等）的 API 或 RPC 节点，实时捕获钱包地址的代币入账事件，利用**金额差异**与**时效性**精确匹配交易归属：

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
