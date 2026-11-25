<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>订阅链上地址，通过 WebSocket、gRPC 或 HTTP 接收相关交易。</strong></p>
<p align="center"><a href="README.md">English</a> · <strong>简体中文</strong> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">AGPL-3.0</a> &nbsp;·&nbsp; 18 条网络</p>
<p align="center"><a href="docs/guide/zh-CN.md#quick-start">快速开始</a> &nbsp;·&nbsp; <a href="docs/guide/zh-CN.md#networks">支持的网络</a> &nbsp;·&nbsp; <a href="docs/api/zh-CN.md">接口文档</a></p>

---

[Bitcoin 主网节点与配置](docs/bitcoin-nodes.zh-CN.md)

## 为什么做 ChainScanner

开发 GM Wallet 和 GMPay 时，我们反复遇到同一个问题。钱包要知道哪些地址需要刷新余额，收款服务要知道哪些地址有新交易。如果每个业务都逐个地址轮询 RPC，即使它们关心的是同一笔交易，也会重复消耗节点配额。

ChainScanner 把监听集中到一个独立服务里。同一条链上的所有订阅共享一次扫块，匹配到的交易会发给每个订阅了相关地址的服务，各自独立确认处理。钱包可以据此只刷新受影响地址的余额，GMPay 可以核验对应订单。一次以太坊主网回放中，读取相同的 4 个区块，监听 1 个地址、100 个地址，以及两个服务同时监听这 100 个地址，都只用了 11 次上游 RPC 请求。

各业务自行维护业务逻辑、订单状态和钱包余额。ChainScanner 提供共享的链上监听基础设施，统一处理地址订阅、区块扫描和事件投递。

> [!IMPORTANT]
> 按哈希查询优先使用兜底节点，取得可用交易内容即返回。Scanner 排除曾经扫到这笔订阅交易的节点。 [查看供应商介绍](#fallback-providers)

## 使用场景

- **链上收款网关。** [EPUSDT 的接入 PR](https://github.com/GMWalletApp/epusdt/pull/127) 订阅商户收款地址，匹配到订单后进入「确认中」，再通过严格查询核验交易后入账。
- **个人地址 Telegram Bot（设想）。** 用户订阅自己的钱包，Bot 保存事件后，根据资金方向推送转入或转出提醒，并附上交易哈希。
- **多钱包对账（设想）。** 财务系统和异常转账提醒服务分别订阅同一组资金地址。每条链只扫一次，两个服务各自消费事件、独立确认处理。

## 订阅一个地址

启动本地服务后，使用快速开始里设置的 `$TOKEN`，将示例地址换成你的 Base 地址。这条请求会替换该消费者的完整订阅列表。

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v1/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

响应中的 `from_height` 是开始监听的第一个区块。 [接收交易并确认处理](docs/api/zh-CN.md#streaming)

## 建议为每条收款链配置兜底节点

每个兜底节点单独登记。失败区块在符合高度要求的兜底节点间轮询补扫。 [API](docs/api/zh-CN.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

<a id="fallback-providers"></a>

### 兜底节点供应商推荐

资料核对日期为 2026-11-21。以下仅供选型参考，请自行选择。ChainScanner 不担保第三方的可用性、数据准确性或价格。Credits、CU、RU 均按供应商和 RPC 方法计量，不能直接换算成请求次数。接入前请分别核对目标链、历史数据和协议支持。

| 官网 | 成立或公开上线时间及背景 | 免费额度 |
| --- | --- | --- |
| [Ankr](https://www.ankr.com/) | [2017 年成立，提供分布式节点基础设施](https://www.ankr.com/ankr-whitepaper-2.0.pdf) | [Freemium 账户每月 2 亿 API credits](https://www.ankr.com/docs/rpc-service/charging-policy/) |
| [Infura](https://www.infura.io/) | [2016 年成立，Consensys 旗下的以太坊与 IPFS 基础设施](https://consensys.io/blog/press-release-infura-a-consensys-company-launches-infura-for-improved-ethereum-infrastructure-support) | [按官方计费文档，Core 免费版每日 300 万 credits](https://docs.infura.io/get-started/pricing/) |
| [PublicNode](https://www.publicnode.com/) | [运营方 Allnodes 自 2018 年提供节点基础设施](https://www.allnodes.com/about/) | [共享公共 RPC，官网未公布固定的单用户月额度](https://www.publicnode.com/) |
| [QuickNode](https://www.quicknode.com/) | [2017 年成立，提供托管多链 RPC 和数据流](https://www.quicknode.com/about) | [30 天试用共 1,000 万 API credits](https://support.quicknode.com/articles/5387074982-1-month-free-trial) |
| [Alchemy](https://www.alchemy.com/) | [2017 年成立，提供区块链开发 API 与节点访问](https://www.alchemy.com/company) | [每月 3,000 万 CU](https://www.alchemy.com/docs/reference/pricing-plans) |
| [BlockPI](https://blockpi.io/) | [2022-10-25 分布式 RPC 网络上线 Alpha 版](https://docs.blockpi.io/welcome-to-blockpi/about-blockpi) | [注册用户每月 5,000 万 RU](https://docs.blockpi.io/pricing/pricing-and-rate-limit) |
| [dRPC](https://drpc.org/) | [2023 年 3 月已开放服务，聚合多家运营商的 RPC 网关](https://blog.drpc.org/how-drpcs-free-tier-works/) | [每 30 天 5,000 万 CU](https://drpc.org/docs/howitworks/ratelimiting) |

## 致使用 ChainScanner 的你

如果你的项目用到了 ChainScanner，或参考了我们的扫块方案，希望你能在项目的 README 里提一句 ChainScanner，附上仓库链接。这份推荐对我们很重要，也能让更多开发者找到这个项目。很高兴能帮到你，谢谢你的使用！

```markdown
扫块方案参考了 [ChainScanner](https://github.com/GMWalletApp/ChainScanner)。
```

感谢 imToken 和 Bitget Wallet 提供公开 RPC 资源，感谢 ChainList、OpenChainBench 和 CompareNodes 维护公开节点目录。也感谢每一位开发者和用户的使用与信任。
