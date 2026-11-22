# 迁移旧版商户接口

[文档目录](README.zh_CN.md) · [English](LEGACY_API_MIGRATION.en.md)

## 确认插件使用的协议

先确认插件（包括独角数卡插件）支持的协议。旧指南曾使用 `/api/v1/order/create-transaction` 或 `/payments/epusdt/v1/order/create-transaction`。当前代码注册的路由如下。只支持旧签名的插件还需升级签名逻辑。

| 协议 | 创建订单 | 请求与回调签名 |
| --- | --- | --- |
| GMPay | `POST /payments/gmpay/v1/order/create-transaction` | HMAC-SHA256 |
| EPay | `GET/POST /payments/epay/v1/order/create-transaction/submit.php` | MD5 |

## 切换前验证

1. 从后台获取商户 PID 与密钥，EPay 使用数字 PID。
2. 通过 `GET /payments/gmpay/v1/config` 查询可用网络和币种。GMPay 如需买家在收银台选择，可同时省略两项。
3. 对照[商户 API 示例](API.md)验算签名，联调创建订单、浏览器跳转与签名回调。重复通知应幂等处理，成功时返回 HTTP 200 及 `ok` 或 `success`。

同时变更数据目录或数据库的部署，请参考[升级说明](https://epusdt.com/zh/guide/installation/docker)。
