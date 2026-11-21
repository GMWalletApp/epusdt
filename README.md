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

## Edge 系列

- **[GMPay Edge](https://github.com/GMWalletApp/gmpay-edge)**  
  基于 Cloudflare Workers 的多链、多币种支付网关，提供商户 API、聚合收银台、支付管理与 Webhook 投递

- **[GMShop Edge](https://github.com/GMWalletApp/gmshop-edge)**  
  基于 Cloudflare Workers 的数字商品商城，支持卡密、私有下载、自动化商品及自动交付
  
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

EPUSDT 向 ChainScanner 订阅收款地址，根据推送中的链、资产、金额和订单有效期匹配订单。

```text
工作流程：
1. 客户发起支付，需支付 20.05 USDT
2. 系统在哈希表中查找可用的钱包地址 + 金额组合
3. 若 address_1:20.05 未被占用 -> 锁定该组合（有效期 10 分钟），返回给客户
4. 若已被占用 -> 自动累加 0.0001 尝试下一个金额组合（最多 100 次）
5. 收到匹配的转账后，订单进入确认中
6. 达到确认深度并通过配置的核验后，订单到账并触发商户回调
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

## OKX OnchainOS 收款

在后台「系统配置 → OKX OnchainOS 配置」填写 API Key、API Secret、API 密码（Passphrase）和商户的 X Layer 收款地址，勾选允许代币后启用。收银台会单独显示 OKX OnchainOS。买家选好代币后，EPUSDT 将订单金额、代币和收款地址发给 OKX，并在当前页面打开该笔订单返回的托管收银台。后端查询到 `settled` 并核对订单后确认到账。

## ChainScanner 地址订阅

EPUSDT 默认在同一进程启动 ChainScanner，自动生成内部凭据，通过随机的本机回环端口通信。扫描游标、订阅和节点设置保存在运行目录的 `chainscanner.sqlite`。区块读取、节点发现、故障转移、缺块补扫和游标均由 ChainScanner 负责。EPUSDT 将启用的收款地址按具体链订阅，在展示新付款地址前检查订阅回执。每条链独立接收活动，按收款地址、资产、原始金额和订单时间窗匹配候选订单。确认任务落库后才 ACK；断线后允许重复投递。建议独立服务配置持久化 `database`，升级时为现有待付订单设置交接回看窗口并核对覆盖范围。

使用独立扫描服务时，在 EPUSDT 进程环境或启动用的 `.env` 填写以下设置，重启后生效。留空 `CHAINSCANNER_URL` 即使用内置扫描器。

| 配置 | 用途 |
| --- | --- |
| `CHAINSCANNER_URL` | 服务的 HTTP(S) 源站，例如同机部署的 `http://127.0.0.1:9081`。容器间使用可达的服务名。 |
| `CHAINSCANNER_TOKEN` | 独立服务分配给 EPUSDT 的消费者令牌。各业务使用不同令牌。 |
| `CHAINSCANNER_ADMIN_TOKEN` | 启用后台节点管理和黑名单所需的独立管理员令牌。 |
| `CHAINSCANNER_TRANSPORT` | 默认 `ws` 推送，也支持 `grpc` 双向流和 `http` 长轮询。gRPC 需要 HTTP/2 或内网 h2c。 |

**后台 → 系统设置 → 链上到账前严格验证** 默认开启。匹配后订单进入确认中。ChainScanner 向两个独立节点核验交易，EPUSDT 根据返回证据重新核对入账和确认数。失败最多两轮，仍失败转人工复核，保留候选哈希并暂停回调。关闭开关仅影响之后新建的候选任务；已有任务和重投保留原核验方式。关闭后仍检查订阅交易、订单匹配和确认深度。TON Jetton 仍按事件所属历史区块校验钱包归属，人工按哈希认领仍执行严格核验。

后台调用下表接口时使用 EPUSDT 管理员 JWT。公共前缀为 `/admin/api/v1/chainscanner`，服务器按路由选用相应的 ChainScanner 令牌。

| 用途 | 方法和相对路径 |
| --- | --- |
| 普通节点、兜底节点 | `GET /admin/nodes`、`POST /admin/nodes`、`POST /admin/nodes/fallback`、`DELETE /admin/nodes/{id}` |
| 首页各链追块延迟 | `GET /admin/progress`（使用扫描器管理员令牌） |
| 手动复测扫描节点 | `POST /admin/nodes/{id}/test`、`POST /admin/nodes/test?network=solana` |
| RPC 地址黑名单 | `GET /admin/blacklist`、`PUT /admin/blacklist/{rule}`、`DELETE /admin/blacklist/{rule}`。例如 `keyword:acme`、`exact:rpc.example.org` |
| 连续进度、普通与严格交易查询 | `GET /heights/{network}`、`GET /transactions/{network}/{hash}`、`GET /transactions/{network}/{hash}/strict` |

ChainScanner 在有订阅时自动探测节点高度，把区块区间分配给高度和确认深度均达标的空闲节点。后台 RPC 节点页可手动复测单个扫描节点或所选链的节点，探测由 ChainScanner 执行。节点列表显示最近检测的高度、延时和状态。首页通过 `GET /admin/progress` 展示各链的可扫高度、连续扫完高度和已观测追块延迟。超过 5 分钟显示警告，无订阅或缺少近期链头会单独标记。查看这两个页面只读取扫描器已有的数据。[节点测试接口](chainscanner/docs/api/zh-CN.md#test-one)

建议为每条收款链配置两家独立的兜底服务商。节点参数、规则格式、订阅边界和跨轮节点排除见 [ChainScanner v1 API 文档](chainscanner/docs/api/zh-CN.md)。

上游为 [GMWalletApp/ChainScanner](https://github.com/GMWalletApp/ChainScanner)。`chainscanner/` 保存完整源码，`chainscanner.rev` 记录上游提交。运行 `scripts/sync-chainscanner <commit>` 可同步指定版本。GitHub Actions 每天检查上游并提交更新 PR，需要在仓库 Actions 设置中允许创建 PR。

新增网络包括 Sui、Arbitrum One、HyperEVM、Monad、Arc 主网、Avalanche C-Chain、OP Mainnet 和 Base 主网。各链预置原生资产和 Circle USDC；Arc 原生币就是 USDC，支付端分别按原生转账的 18 位和 ERC-20 的 6 位解析。Sui 按完整 Move coin type 区分资产。同名跨链资产通过实际到账的链和合约或 coin type 匹配订单。

EVM 优先使用支持普通 RPC 读取的 WSS 节点，收到新区块头即按持久化游标补齐；没有可用 WSS 时使用 HTTPS。Sui 使用复用的 gRPC 连接监听检查点。Base 默认支持 ETH 和 Circle 原生 USDC。Base、Arbitrum One 和 OP Mainnet 的确认数以 L2 区块计算。支付端按网络独立消费，一条链的支付处理重试不会阻塞其他链。独立服务的 `PUT /v1/subscriptions` 返回订阅起点 `from_height` 和退订终点 `to_height`（两端均含）。退订返回 202 时，可重发同一份完整订阅列表查询完成状态，已入队事件仍可领取，接口示例见 ChainScanner README。

扫块节点和兜底节点通过 ChainScanner 节点管理接口配置。原有 RPC 配置仍供人工验单及特定链的按笔读取使用。各链协议和历史节点配置见 [ChainScanner 节点文档](chainscanner/docs/api/zh-CN.md)。
