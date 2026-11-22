# Migrate a legacy merchant integration

[Documentation index](README.md) · [简体中文](LEGACY_API_MIGRATION.md)

## Choose the integration used by your plugin

Identify the protocol supported by your plugin, including Dujiaoka plugins. Earlier guides used `/api/v1/order/create-transaction` or `/payments/epusdt/v1/order/create-transaction`. Current builds register the routes below. A plugin that only knows the old signature needs an update as well as a new URL.

| Integration | Create order | Request and callback signatures |
| --- | --- | --- |
| GMPay | `POST /payments/gmpay/v1/order/create-transaction` | HMAC-SHA256 |
| EPay | `GET/POST /payments/epay/v1/order/create-transaction/submit.php` | MD5 |

## Verify before switching traffic

1. Obtain the merchant PID and secret from the admin console. Use a numeric PID for EPay.
2. Query `GET /payments/gmpay/v1/config` for supported network/token pairs. For GMPay, omit both values if the buyer will select them at checkout.
3. Check the signature against the [merchant API examples](API.en.md). Test order creation, the browser redirect, and the signed merchant notification. Accept repeated notifications idempotently and acknowledge success with HTTP 200 and `ok` or `success`.

For deployments also changing their data directories or database engine, follow the [upgrade notes](https://epusdt.com/guide/installation/docker).
