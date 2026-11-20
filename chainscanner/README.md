<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Watch blockchain addresses and receive their transactions over WebSocket, gRPC or HTTP.</strong></p>
<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 17 networks</p>
<p align="center"><a href="docs/guide/en.md#quick-start">Quick start</a> &nbsp;·&nbsp; <a href="docs/guide/en.md#networks">Networks</a> &nbsp;·&nbsp; <a href="docs/api/en.md">API reference</a></p>

---

## Use cases

- **On-chain checkout.** The [EPUSDT integration](https://github.com/GMWalletApp/epusdt/pull/127) subscribes to deposit addresses, marks a matching order as confirming and checks the transaction with two nodes before crediting it.
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

For each network you accept payments on, register a reliable fallback from a different operator than your other nodes. It can repair a failed block read and gets priority for one of the two strict transaction lookup slots. If two rounds fail, send the payment to manual review. [API](docs/api/en.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

## A note from the maintainers

If your project uses ChainScanner or builds on its design, we’d appreciate a link in your README. Your recommendation helps other developers find us and means a lot to our team. We’re glad we could help. Thank you for using ChainScanner!

```markdown
Reference: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```

Thanks to imToken and Bitget Wallet for public RPC resources, and to ChainList, OpenChainBench and CompareNodes for their public node directories. Thank you to everyone who uses and trusts ChainScanner.
