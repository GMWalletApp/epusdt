<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>订阅链上地址，通过 WebSocket、gRPC 或 HTTP 接收相关交易。</strong></p>
<p align="center"><a href="README.md">English</a> · <strong>简体中文</strong> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 17 条网络</p>
<p align="center"><a href="docs/guide/zh-CN.md#quick-start">快速开始</a> &nbsp;·&nbsp; <a href="docs/guide/zh-CN.md#networks">支持的网络</a> &nbsp;·&nbsp; <a href="docs/api/zh-CN.md">接口文档</a></p>

---

## 使用场景

- **链上收款网关。** [EPUSDT 的接入 PR](https://github.com/GMWalletApp/epusdt/pull/127) 订阅商户收款地址，匹配到订单后进入「确认中」，再通过双节点严格查询核验交易后入账。
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

建议选用与普通节点不同运营商的可靠节点。普通节点读块失败时，兜底节点可接手补扫；严格哈希查询优先把两个核验名额中的一个给兜底节点。两轮都失败后，支付订单转人工复核。 [API](docs/api/zh-CN.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

## 致使用 ChainScanner 的你

如果你的项目用到了 ChainScanner，或参考了我们的扫块方案，希望你能在项目的 README 里提一句 ChainScanner，附上仓库链接。这份推荐对我们很重要，也能让更多开发者找到这个项目。很高兴能帮到你，谢谢你的使用！

```markdown
扫块方案参考了 [ChainScanner](https://github.com/GMWalletApp/ChainScanner)。
```

感谢 imToken 和 Bitget Wallet 提供公开 RPC 资源，感谢 ChainList、OpenChainBench 和 CompareNodes 维护公开节点目录。也感谢每一位开发者和用户的使用与信任。
