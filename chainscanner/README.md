<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Watch blockchain addresses and receive their transactions over WebSocket, gRPC or HTTP.</strong></p>
<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">AGPL-3.0</a> &nbsp;·&nbsp; 18 networks</p>
<p align="center"><a href="docs/guide/en.md#quick-start">Quick start</a> &nbsp;·&nbsp; <a href="docs/guide/en.md#networks">Networks</a> &nbsp;·&nbsp; <a href="docs/api/en.md">API reference</a></p>

---

[Bitcoin mainnet endpoints and configuration](docs/bitcoin-nodes.zh-CN.md)

## Why we built ChainScanner

While developing GM Wallet and GMPay, we kept running into the same problem. The wallet needed to know which balances to refresh, and the payment service needed to know when a receiving address had new activity. Polling RPC endpoints for each address in each app spent quota twice when both apps were looking for the same transaction.

ChainScanner gives all subscribers on a network a shared block scan. Each service receives transactions that match its addresses and acknowledges them independently. A wallet can refresh just the affected balances, while GMPay can verify the corresponding orders. In an Ethereum mainnet replay, reading the same four blocks took 11 upstream RPC calls when watching one address, when watching 100 addresses, and when two services watched those same 100 addresses.

Each application manages its own business logic, order status and wallet balances. ChainScanner provides shared infrastructure for address subscriptions, block scanning and event delivery.

> [!IMPORTANT]
> Hash lookup prefers a fallback node and returns the first usable transaction. The scanner excludes the sources that supplied the matching block activity. [Compare fallback providers](#fallback-providers)

## Use cases

- **On-chain checkout.** The [EPUSDT integration](https://github.com/GMWalletApp/epusdt/pull/127) subscribes to deposit addresses, marks a matching order as confirming and checks the transaction through the strict lookup before crediting it.
- **Telegram wallet bot idea.** Subscribe to a personal address, record each delivery, then send a message identifying an incoming or outgoing transfer.
- **Treasury reconciliation idea.** Watch operational wallets once and deliver the same activity to accounting and alerting as separate consumers, each with its own acknowledgement.

## Subscribe to an address

With a local server running, use the consumer token from the quick start as `$TOKEN`. Replace the example with your Base address. This request sets the consumer’s entire watch list.

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v1/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

The response includes `from_height`, the first block covered. [Receive and acknowledge transactions](docs/api/en.md#streaming)

## Recommended fallback node

Failed ranges rotate across eligible fallback nodes. Register each node separately. [API](docs/api/en.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

<a id="fallback-providers"></a>

### Fallback providers

Provider shortlist, checked 2026-11-21. Choose independently. ChainScanner does not guarantee availability, data accuracy or pricing. Credits, CU and RU depend on the RPC method and are not interchangeable. Check network, history and protocol support for both endpoints.

| Website | Founded or first public release | Free allowance |
| --- | --- | --- |
| [Ankr](https://www.ankr.com/) | [2017 · distributed node infrastructure](https://www.ankr.com/ankr-whitepaper-2.0.pdf) | [200M API credits/month with a Freemium account](https://www.ankr.com/docs/rpc-service/charging-policy/) |
| [Infura](https://www.infura.io/) | [2016 · Consensys Ethereum and IPFS infrastructure](https://consensys.io/blog/press-release-infura-a-consensys-company-launches-infura-for-improved-ethereum-infrastructure-support) | [3M credits/day on Core, per the pricing documentation](https://docs.infura.io/get-started/pricing/) |
| [PublicNode](https://www.publicnode.com/) | [2018 · operator Allnodes began offering node infrastructure](https://www.allnodes.com/about/) | [Shared public RPC, no fixed per-user monthly allowance published](https://www.publicnode.com/) |
| [QuickNode](https://www.quicknode.com/) | [2017 · managed multichain RPC and data streams](https://www.quicknode.com/about) | [10M API credits for a 30-day trial](https://support.quicknode.com/articles/5387074982-1-month-free-trial) |
| [Alchemy](https://www.alchemy.com/) | [2017 · blockchain developer APIs and node access](https://www.alchemy.com/company) | [30M CU/month](https://www.alchemy.com/docs/reference/pricing-plans) |
| [BlockPI](https://blockpi.io/) | [2022-10-25 · distributed RPC network alpha launch](https://docs.blockpi.io/welcome-to-blockpi/about-blockpi) | [50M RU/month for registered users](https://docs.blockpi.io/pricing/pricing-and-rate-limit) |
| [dRPC](https://drpc.org/) | [2023-03 · decentralized RPC gateway opened to the public](https://blog.drpc.org/how-drpcs-free-tier-works/) | [50M CU per 30 days](https://drpc.org/docs/howitworks/ratelimiting) |

## A note from the maintainers

If your project uses ChainScanner or builds on its design, we’d appreciate a link in your README. Your recommendation helps other developers find us and means a lot to our team. We’re glad we could help. Thank you for using ChainScanner!

```markdown
Reference: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```

Thanks to imToken and Bitget Wallet for public RPC resources, and to ChainList, OpenChainBench and CompareNodes for their public node directories. Thank you to everyone who uses and trusts ChainScanner.
