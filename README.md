# EPUSDT — Easy Payment USDT

<img src="./src/www/images/logo.png" alt="EPUSDT" width="100">

EPUSDT 是用 Go 编写的自托管加密支付网关。商户通过 GMPay 或易支付兼容接口创建订单，买家向商户钱包转账，EPUSDT 核验到账后回调商户。 支持多个收款地址轮换，也可以通过 Telegram Bot 接收支付通知。

[安装指南](wiki/EPCTL.md) · [商户 API](wiki/API.md) · [下载发行版](https://github.com/GMWalletApp/epusdt/releases) · [English](README.en.md)

## 已支持网络与代币

下表列出新安装预置的 16 条直接收款网络。收银台只展示本实例已启用、有可用收款地址和有效币种配置的组合。对接时读取 `GET /payments/gmpay/v1/config` 的 `data.supported_assets`，即可获得当前可用的网络标识和币种。

| 网络 | API 网络标识 | 默认收款币种 |
| --- | --- | --- |
| TRON | `tron` | TRX、USDT |
| Ethereum | `ethereum` | USDT、USDC |
| BNB Smart Chain | `binance` | USDT、USDC |
| Polygon PoS | `polygon` | USDT、USDC、USDC.e |
| Solana | `solana` | SOL、USDT、USDC |
| TON | `ton` | TON、USDT |
| Plasma | `plasma` | USDT |
| Aptos | `aptos` | USDT、USDC |
| Sui | `sui` | SUI、USDC |
| Arbitrum One | `arbitrum` | ETH、USDC |
| Base | `base` | ETH、USDC |
| OP Mainnet | `optimism` | ETH、USDC |
| Avalanche C-Chain | `avalanche` | AVAX、USDC |
| HyperEVM | `hyperevm` | HYPE、USDC |
| Monad | `monad` | MON、USDC |
| Arc | `arc` | USDC |

Polygon 的 USDC 和 USDC.e 是两种不同资产。Plasma 的预置收款币种在 EPUSDT 中标记为 `USDT`。Arc 的原生转账和 ERC-20 转账都以 USDC 收款。

## 托管收银台

这两种支付方式需由商户单独启用。公开配置接口分别通过 `xlayer` 和 `okpay` 返回其状态和允许的币种。

- **OKX OnchainOS（X Layer）** 支持 USDC、USDT0、USDG。在后台填写 OKX API Key、Secret、Passphrase 和 X Layer 收款地址，选择接受的币种。买家在 OKX 页面完成支付，EPUSDT 查询到 `settled` 并核对订单后确认到账。
- **OKPay** 默认允许 USDT、TRX。收银台按商户配置显示可用币种。

## 链上收款流程

1. 创建订单后，EPUSDT 分配收款地址和应付金额，在订单有效期内保留这组地址与金额。
2. ChainScanner 按链订阅启用的收款地址。匹配网络、币种、金额和付款时间的转账会让订单进入「确认中」。
3. 到账前严格核验默认开启。两个独立节点的结果一致、收款信息与确认数通过检查后，EPUSDT 确认到账并回调商户。连续两轮核验失败，订单转人工复核，保留交易哈希并暂停成功回调。

## 部署与升级

Linux 二进制安装和升级使用 [epctl 指南](wiki/EPCTL.md)。也可以选择 [Docker Compose](docker-compose.yaml)、[宝塔面板](https://epusdt.com/guide/installation/aapanel) 或[手动部署](https://epusdt.com/guide/installation/manual.html)。从 v2.0.0 升级 Docker Compose 时，先备份并迁移数据到新的挂载目录，再重建容器。[v2.1.0 升级说明](https://github.com/GMWalletApp/epusdt/releases/tag/v2.1.0)

### 数据库与升级

EPUSDT 仅支持 SQLite。新安装可省略 `db_type`，也可以显式设置 `db_type=sqlite`。首次启动会自动建表。

| 用途 | 配置项 | 默认位置 |
| --- | --- | --- |
| 业务数据（订单、钱包、商户配置） | `sqlite_database_filename` | 配置文件同目录的 `epusdt.db` |
| 运行状态（交易锁等） | `runtime_sqlite_filename` | `runtime_root_path` 下的 `epusdt-runtime.db` |

升级时请备份配置和这两个数据库。使用内置 ChainScanner 时，也要保留运行目录中的 `chainscanner.sqlite`，其中存有订阅、扫描进度和节点设置。旧版 MySQL 或 PostgreSQL 部署需先备份、自行迁移并核对数据，再切换到 SQLite。修改 `db_type` 不会转换历史数据。

## ChainScanner 地址订阅

默认随 EPUSDT 在同一进程启动，无需另行配置凭据。扫描器通过多个节点读取区块，故障时切换节点并补扫缺块。后台可查看各链已扫高度、目标高度和追块延迟，超过 5 分钟会提示。

<details>
<summary>独立部署与节点管理</summary>

原先使用独立扫描容器的部署可保留 `./runtime/chainscanner`、既有令牌和连接地址，使用 `docker compose --profile scanner up -d` 继续读取原有订阅和游标。新建独立扫描容器时设置两枚不同的令牌，支付服务使用 `CHAINSCANNER_URL=http://chainscanner:9081`。

使用独立扫描服务时，在 EPUSDT 进程环境或启动用的 `.env` 填写以下设置，重启后生效。留空 `CHAINSCANNER_URL` 即使用内置扫描器。

| 配置 | 用途 |
| --- | --- |
| `CHAINSCANNER_URL` | 服务的 HTTP(S) 源站，例如同机部署的 `http://127.0.0.1:9081`。容器间使用可达的服务名。 |
| `CHAINSCANNER_TOKEN` | 独立服务分配给 EPUSDT 的消费者令牌。各业务使用不同令牌。 |
| `CHAINSCANNER_ADMIN_TOKEN` | 启用后台节点管理和黑名单所需的独立管理员令牌。 |
| `CHAINSCANNER_TRANSPORT` | 默认 `ws` 推送，也支持 `grpc` 双向流和 `http` 长轮询。gRPC 需要 HTTP/2 或内网 h2c。 |

后台接口使用 EPUSDT 管理员 JWT。公共前缀为 `/admin/api/v1/chainscanner`，服务器按路由选用相应的 ChainScanner 令牌。

| 用途 | 方法和相对路径 |
| --- | --- |
| 普通节点、兜底节点 | `GET /admin/nodes`、`POST /admin/nodes`、`POST /admin/nodes/fallback`、`DELETE /admin/nodes/{id}` |
| 首页各链追块延迟 | `GET /admin/progress`（使用扫描器管理员令牌） |
| 手动复测扫描节点 | `POST /admin/nodes/{id}/test`、`POST /admin/nodes/test?network=solana` |
| RPC 地址黑名单 | `GET /admin/blacklist`、`PUT /admin/blacklist/{rule}`、`DELETE /admin/blacklist/{rule}`。例如 `keyword:acme`、`exact:rpc.example.org` |
| 连续进度、普通与严格交易查询 | `GET /heights/{network}`、`GET /transactions/{network}/{hash}`、`GET /transactions/{network}/{hash}/strict` |

可为每条链配置两家独立服务商提供的兜底节点，用于补扫和严格核验。[ChainScanner API](chainscanner/docs/api/zh-CN.md) · [上游仓库](https://github.com/GMWalletApp/ChainScanner) · [本项目使用的版本](chainscanner.rev)

</details>

## 集成与社区

| 领域 | 已支持系统 |
|------|-----------|
| **AI 分发** | [OneAPI](https://github.com/songquanpeng/one-api)、[Sub2API](https://github.com/Wei-Shaw/sub2api)、[NewAPI](https://github.com/QuantumNous/new-api) |
| **发卡系统** | [独角数卡（Dujiaoka）](https://dujiao-next.com/)、[异次元发卡](https://github.com/lizhipay/acg-faka) |
| **代理面板** | [V2Board](https://github.com/v2board/v2board)、[XBoard](https://github.com/cedar2025/Xboard)、[xiaoV2board](https://github.com/wyx2685/v2board/)、[SSPanel](https://github.com/anankke/sspanel-uim) |
| **建站生态** | [WordPress](https://wordpress.com/)、[WHMCS](https://www.whmcs.com/) |
| **Epay 兼容** | 兼容各类支持 Epay 易支付接口的平台 |

[官方文档](https://epusdt.com) · [问题反馈](https://github.com/GMWalletApp/epusdt/issues) · [Telegram 频道](https://t.me/epusdt) · [Telegram 交流群](https://t.me/epusdt_group)

相关项目：[GMPay Edge 支付网关](https://github.com/GMWalletApp/gmpay-edge) 和 [GMShop Edge 数字商品商城](https://github.com/GMWalletApp/gmshop-edge) 均运行在 Cloudflare Workers 上。

<details>
<summary>后台和收银台截图</summary>

<img src="wiki/img/web2.png" alt="后台首页" width="210">
<img src="wiki/img/web1.png" alt="管理面板" width="210">
<img src="wiki/img/pay1.jpeg" alt="收银台" width="210">
<img src="wiki/img/pay2.jpeg" alt="支付页面" width="210">

</details>

<details>
<summary>Star History</summary>

<a href="https://www.star-history.com/?type=date&repos=gmwalletapp%2Fepusdt">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
 </picture>
</a>

</details>

## 安全审计与许可

[VectorBits 安全审计报告（2026 年 5 月）](https://github.com/VectorBits/audit/blob/main/epusdt-secure-audit-report-2026-05-14.pdf) · [GPLv3 开源协议](LICENSE)

<details>
<summary>免责声明及使用条款</summary>

EPusdt 由 Good Morning Technology, LLC 以免费、开源、非盈利及非托管性质开发和披露，仅供学习、研究与技术交流使用。项目本身不构成投资、金融、法律、税务、合规或任何其他专业建议，也不应被视为对任何资产、交易结果、收益、资金安全、技术可用性或特定用途作出任何保证。

Good Morning Technology, LLC 为依据美国法律设立的主体，并将在适用法律法规范围内履行相应合规义务。就美国监管框架而言，虚拟货币、数字资产及相关技术服务是否构成 money transmission、money services business（MSB）或其他受监管活动，通常取决于具体业务模式，包括项目方是否接收、持有、控制或传输资金或价值，是否代表用户托管资产，以及是否从事兑换、支付、清算、结算或其他中介性金融服务。

作为免费、开源、非盈利、非托管的软件项目，EPusdt 的代码披露、文档说明、技术交流及相关开发活动，本身不应被理解为 Good Morning Technology, LLC 或其贡献者对任何用户后续使用、修改、部署、集成、分发或二次开发行为的授权、背书、控制、参与、保证或承诺。

用户对本项目的实际使用方式、使用目的及后续行为由其自行决定，Good Morning Technology, LLC 及其贡献者无法控制、审查或限制该等行为。用户应自行确保其使用、修改、部署、集成、分发或二次开发行为符合所在地适用法律法规、监管要求、制裁规则及第三方权利，并独立承担由此产生的全部风险、责任与后果。

加密资产属于高风险新兴资产类别，包括稳定币在内的数字资产均可能发生剧烈波动、脱锚、流动性不足、技术故障、监管变化或价值归零等风险。本项目所有代码、文档及相关材料均按“现状”和“可用状态”提供。除适用法律另有强制规定外，Good Morning Technology, LLC 及其贡献者不因用户使用、无法使用、错误使用、违法使用、修改、部署、集成、分发、二次开发或依赖本项目而产生的任何直接或间接损失承担责任。

</details>
