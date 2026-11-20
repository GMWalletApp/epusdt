<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt=""></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>订阅链上地址，通过 WebSocket、gRPC 或 HTTP 接收相关交易。</strong></p>
<p align="center"><a href="README.md">English</a> · <strong>简体中文</strong> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 18 条网络</p>
<p align="center"><a href="docs/guide/zh-CN.md#quick-start">快速开始</a> &nbsp;·&nbsp; <a href="docs/guide/zh-CN.md#networks">支持的网络</a> &nbsp;·&nbsp; <a href="docs/api/zh-CN.md">接口文档</a></p>

---

## 订阅一个地址

启动本地服务后，使用快速开始里设置的 `$TOKEN`，将示例地址换成你的 Base 地址。这条请求会替换该消费者的完整订阅列表。

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v2/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

响应中的 `from_height` 是开始监听的第一个区块。 [接收交易并确认处理](docs/api/zh-CN.md#streaming)

## 致使用 ChainScanner 的你

如果你的项目用到了 ChainScanner，或参考了我们的扫块方案，希望你能在项目的 README 里提一句 ChainScanner，附上仓库链接。这份推荐对我们很重要，也能让更多开发者找到这个项目。很高兴能帮到你，谢谢你的使用！

```markdown
扫块方案参考了 [ChainScanner](https://github.com/GMWalletApp/ChainScanner)。
```
