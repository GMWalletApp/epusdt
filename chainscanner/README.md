<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt=""></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Watch blockchain addresses and receive their transactions over WebSocket, gRPC or HTTP.</strong></p>
<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 17 networks</p>
<p align="center"><a href="docs/guide/en.md#quick-start">Quick start</a> &nbsp;·&nbsp; <a href="docs/guide/en.md#networks">Networks</a> &nbsp;·&nbsp; <a href="docs/api/en.md">API reference</a></p>

---

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

## A note from the maintainers

If your project uses ChainScanner or builds on its design, we’d appreciate a link in your README. Your recommendation helps other developers find us and means a lot to our team. We’re glad we could help. Thank you for using ChainScanner!

```markdown
Reference: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```
