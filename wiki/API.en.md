# Merchant API

[Documentation index](README.md) · [简体中文](API.md)

Use the public configuration endpoint to discover payment options, create an order on your server, and send the buyer to the returned checkout URL. Verify the signed notification before fulfilling the order. [Python order example](../sdk/python/gmpay_create_order.py) · [OpenAPI specification](../src/docs/swagger.json)

[Sign a request](#signing) · [Create a GMPay order](#create) · [Poll status](#status) · [Merchant callbacks](#callbacks)

## Endpoints

| Purpose | Method | Path | Authentication |
| --- | --- | --- | --- |
| Create a GMPay order | POST | `/payments/gmpay/v1/order/create-transaction` | Merchant HMAC-SHA256 |
| Read public checkout settings | GET | `/payments/gmpay/v1/config` | Public |
| Open checkout | GET | `/pay/checkout-counter/{trade_id}` | Public |
| Read checkout details | GET | `/pay/checkout-counter-resp/{trade_id}` | Public |
| Poll order status | GET | `/pay/check-status/{trade_id}` | Public |
| Select network or provider | POST | `/pay/switch-network` | Public |
| Submit a transfer hash for verification | POST | `/pay/submit-tx-hash/{trade_id}` | Public |
| Return from checkout | GET | `/pay/return/{trade_id}` | Browser redirect |
| Create an EPay order | GET or POST | `/payments/epay/v1/order/create-transaction/submit.php` | Merchant MD5 |
| Receive an OKPay provider notification | POST | `/payments/okpay/v1/notify` | OKPay signature |

## Response format

JSON endpoints return `status_code` (200 on success), `message`, `data` (object or null), and the server-generated `request_id`. Invalid signatures return HTTP 401. Business errors generally return HTTP 400 with an application error code.

```json
{
  "status_code": 200,
  "message": "success",
  "data": {},
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

<a id="signing"></a>

## Sign a request

Use the merchant key associated with the request’s `pid`. A new installation seeds a default merchant with PID `1000`. Keep the secret on the merchant server.

**GMPay** uses HMAC-SHA256. Drop `signature`, empty strings and null values. Sort the remaining case-sensitive field names in ascending ASCII order. Join the unescaped `key=value` pairs with `&`, use `secret_key` as the HMAC key and encode the digest as 64 lowercase hexadecimal characters. Include `pid` and any nonempty `payment_type`. With JSON, the server reads a number such as `100.00` as `100`. Form encoding lets you supply the original string representation.

Example fields, canonical string and signature using the demonstration key `epusdt_secret_key`:

```text
pid=1000
order_id=ORD202605230001
currency=cny
token=usdt
network=tron
amount=100
notify_url=https://merchant.example/notify
redirect_url=https://merchant.example/return
name=VIP
```

```text
amount=100&currency=cny&name=VIP&network=tron&notify_url=https://merchant.example/notify&order_id=ORD202605230001&pid=1000&redirect_url=https://merchant.example/return&token=usdt
```

```text
signature=6f874b1919d95081835e2809b620e354a5866f5a6dbb2e432d1627f1eb10059d
```

### PHP examples

GMPay:

```php
function gmpaySign(array $params, string $secretKey): string
{
    unset($params['signature']);
    ksort($params, SORT_STRING);

    $pairs = [];
    foreach ($params as $key => $value) {
        if ($value === '' || $value === null) {
            continue;
        }
        $pairs[] = $key . '=' . $value;
    }

    return hash_hmac('sha256', implode('&', $pairs), $secretKey);
}
```

EPay excludes `sign` and `sign_type`, appends the merchant secret to the canonical string and uses MD5:

```php
function epaySign(array $params, string $secretKey): string
{
    unset($params['sign'], $params['sign_type']);
    ksort($params, SORT_STRING);

    $pairs = [];
    foreach ($params as $key => $value) {
        if ($value === '' || $value === null) {
            continue;
        }
        $pairs[] = $key . '=' . $value;
    }

    return strtolower(md5(implode('&', $pairs) . $secretKey));
}
```

<a id="create"></a>

## Create a GMPay order

`POST /payments/gmpay/v1/order/create-transaction` accepts JSON and `application/x-www-form-urlencoded`.

```json
{
  "pid": "1000",
  "order_id": "ORD202605230001",
  "currency": "cny",
  "token": "usdt",
  "network": "tron",
  "amount": 100,
  "notify_url": "https://merchant.example/notify",
  "redirect_url": "https://merchant.example/return",
  "name": "VIP",
  "signature": "6f874b1919d95081835e2809b620e354a5866f5a6dbb2e432d1627f1eb10059d"
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `pid` | string | Yes | Merchant PID used to look up the signing key. |
| `order_id` | string | Yes | Unique merchant order ID, up to 32 characters. |
| `currency` | string | Yes | Fiat currency, for example `usd` or `cny`. |
| `token` | string | Conditional | Receiving asset. Pair with network or omit both. |
| `network` | string | Conditional | Network ID from public config, for example `binance`. |
| `amount` | number | Yes | Fiat amount, greater than 0.01. |
| `notify_url` | string | Yes | Asynchronous success callback URL. |
| `redirect_url` | string | No | Browser return URL. |
| `name` | string | No | Product or order name. |
| `payment_type` | string | No | Defaults to GMPay. EPay (case-insensitive) selects EPay callback format and requires a numeric PID. Include any nonempty value in the signature. |
| `signature` | string | Yes | 64-character lowercase HMAC-SHA256 signature. |

Pass both `token` and `network` to allocate an address immediately. Omit both to create a placeholder (status 4) containing the fiat amount and currency. Passing only one is invalid. Check `data.supported_assets` in public config before selecting a pair.

### Response

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "trade_id": "20260523171652123456001",
    "order_id": "ORD202605230001",
    "amount": 100,
    "currency": "CNY",
    "actual_amount": 14.29,
    "receive_address": "TTestTronAddress001",
    "token": "USDT",
    "status": 1,
    "expiration_time": 1779530812,
    "payment_url": "https://pay.example.com/pay/checkout-counter/20260523171652123456001"
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `trade_id` | string | EPUSDT transaction ID. |
| `order_id` | string | Merchant order ID. |
| `amount` | number | Fiat amount submitted. |
| `currency` | string | Fiat currency. |
| `actual_amount` | number | Crypto amount due. |
| `receive_address` | string | Receiving address. |
| `token` | string | Asset symbol. |
| `status` | integer | Order state. A value of 4 means awaiting selection. |
| `expiration_time` | integer | Expiration as a Unix timestamp in seconds. |
| `payment_url` | string | URL to open in a browser. |

A placeholder has no address or token and an `actual_amount` of zero. Expiration or manual closure moves it to status 3 without releasing a payment reservation. The checkout selection determines the address or hosted URL.

## Public checkout configuration

`GET /payments/gmpay/v1/config` returns the checkout branding, available on-chain assets, EPay defaults, and the public `okpay` and `xlayer` provider settings. Each entry in `supported_assets` requires an enabled chain, an available receiving address and an enabled token configured for payment. Hosted methods expose their own `enabled` flag and `allow_tokens` list.

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "supported_assets": [
      {
        "network": "tron",
        "display_name": "TRON",
        "tokens": ["TRX", "USDT"]
      },
      {
        "network": "solana",
        "display_name": "Solana",
        "tokens": ["SOL", "USDC", "USDT"]
      }
    ],
    "site": {
      "cashier_name": "Acme Cashier",
      "logo_url": "https://cdn.example.com/logo.png",
      "website_title": "Acme Payments",
      "support_link": "https://example.com/support",
      "background_color": "#0f172a",
      "background_image_url": "https://cdn.example.com/background.png",
      "withdrawal_reminder_enabled": true
    },
    "epay": {
      "default_token": "",
      "default_currency": "cny",
      "default_network": ""
    },
    "okpay": {
      "enabled": false,
      "allow_tokens": ["USDT", "TRX"]
    },
    "xlayer": {
      "enabled": false,
      "allow_tokens": ["USDC", "USDT0", "USDG"]
    },
    "version": "v2.1.0"
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

### Exchange withdrawal reminder

After loading config, show the reminder only when `site.withdrawal_reminder_enabled` is true, the on-chain order is awaiting payment, a receiving address is available and `actual_amount > 0`. Treat a missing flag on older versions as true. Display the expected **net received amount** after exchange fees using the same precision and token as the order. [Light preview](img/withdrawal-reminder-light.png) · [Dark preview](img/withdrawal-reminder-dark.png)

Administrators read `GET /admin/api/v1/settings?group=brand`. The stored value of `brand.withdrawal_reminder_enabled` is the string `"true"` or `"false"`. To change it, send the following authenticated `PUT /admin/api/v1/settings` request. Check the returned item’s `ok` flag and reopen checkout.

```json
{"items":[{"group":"brand","key":"brand.withdrawal_reminder_enabled","type":"bool","value":false}]}
```

## Open checkout and read the order

The `payment_url` returned at creation points to `GET /pay/checkout-counter/{trade_id}`. Opening it in a browser returns a 301 redirect to:

```text
/cashier/{trade_id}
```

Fetch `GET /pay/checkout-counter-resp/{trade_id}` for display details. This response uses **milliseconds** for `created_at` and `expiration_time`. Poll the status endpoint for subsequent changes.

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "trade_id": "20260523171652123456001",
    "amount": 100,
    "actual_amount": 14.29,
    "token": "USDT",
    "currency": "CNY",
    "receive_address": "TTestTronAddress001",
    "network": "tron",
    "status": 1,
    "payment_type": "gmpay",
    "expiration_time": 1779530812000,
    "redirect_url": "https://merchant.example/return",
    "payment_url": "",
    "created_at": 1779530212000,
    "is_selected": false
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

For a placeholder the same parent ID is returned with empty on-chain fields. Either GMPay or EPay can create this state:

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "trade_id": "20260523171652123456001",
    "amount": 100,
    "actual_amount": 0,
    "token": "",
    "currency": "CNY",
    "receive_address": "",
    "network": "",
    "status": 4,
    "payment_type": "gmpay",
    "expiration_time": 1779530812000,
    "redirect_url": "https://merchant.example/return",
    "payment_url": "",
    "created_at": 1779530212000,
    "is_selected": false
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

Checkout returns `payment_type` in lowercase (`gmpay` or `epay`), which selects the merchant callback format. For OKX OnchainOS on X Layer, open the hosted URL returned by the server. EPUSDT checks the provider’s settled order before crediting it.

<a id="status"></a>

## Poll status

`GET /pay/check-status/{trade_id}`

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "trade_id": "20260523171652123456001",
    "status": 1
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

| Status | Meaning |
| --- | --- |
| 1 | Awaiting payment |
| 2 | Paid |
| 3 | Expired |
| 4 | Awaiting a network, asset or provider selection |
| 5 | A matching transfer has been observed and is confirming |
| 6 | Two rounds of strict verification failed. Manual review is required |

At status 5, continue polling and show “Confirming” so the buyer knows the transfer was seen. By default EPUSDT checks the required confirmation depth and verifies the transaction with two independent nodes before crediting it. Waiting for depth does not consume an attempt. Verification attempts and the first set of node IDs persist across restarts. At status 6, automatic verification and success callbacks pause. An administrator can inspect `review_transaction_hash` and recheck the transaction after investigating.

For a buyer-supplied hash, send `block_transaction_id` to `POST /pay/submit-tx-hash/{trade_id}`. The server verifies the transfer. The browser return route, `GET /pay/return/{trade_id}`, sends an HTTP 302 redirect.

## Switch payment network or provider

`POST /pay/switch-network` accepts a parent `trade_id`, target `token` and `network`. For hosted checkout select `okpay` or `xlayer` with a token allowed by that provider.

```json
{
  "trade_id": "20260523171652123456001",
  "token": "USDT",
  "network": "solana"
}
```

```json
{
  "trade_id": "20260523171652123456001",
  "token": "USDT",
  "network": "okpay"
}
```

- Only a pending parent order (status 1 or 4) can switch. A child cannot switch again.
- The first selection on a placeholder completes the same parent order and moves it to status 1. On-chain checkout now allocates an address and amount. Hosted checkout returns the provider’s payment URL. For OKPay the receiving address field contains `OKPAY`.
- The completed placeholder initially keeps `is_selected=false`. Selecting the same target again returns the existing order and marks it selected. Switching to a different target can create at most one child per parent.
- The response has the checkout-detail shape. On-chain orders have an empty hosted `payment_url`.

## EPay-compatible checkout

Send a GET query or a POST form to `/payments/epay/v1/order/create-transaction/submit.php`. Success returns HTTP 302 to:

```text
/pay/checkout-counter/{trade_id}
```

| Field | Location | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `pid` | query/form | string | Yes | Merchant PID. Use a numeric PID for callbacks. |
| `money` | query/form | number | Yes | Fiat amount. |
| `out_trade_no` | query/form | string | Yes | Merchant order ID. |
| `notify_url` | query/form | string | Yes | Merchant callback URL. |
| `return_url` | query/form | string | No | Browser return URL. |
| `name` | query/form | string | No | Product or order name. |
| `type` | query/form | string | No | Empty, `alipay`, or an enabled `token.network` selector. |
| `token` | query/form | string | No | Requested token when no selector matched. |
| `network` | query/form | string | No | Requested network when no selector matched. |
| `currency` | query/form | string | No | Overrides the EPay currency default. |
| `sign` | query/form | string | Yes | EPay MD5 signature. |
| `sign_type` | query/form | string | No | Typically `MD5`. |

Include every nonempty incoming parameter (including plugin fields such as `sitename`) except `sign` and `sign_type` in the MD5 calculation. Example canonical input including the demonstration secret, followed by its signature:

```text
money=100&name=VIP&notify_url=https://merchant.example/notify&out_trade_no=ORD202605230001&pid=1000&return_url=https://merchant.example/return&type=alipayepusdt_secret_key
```

```text
sign=b865b0acbb2b01554c35a1bd33351452
```

**Asset selection.** A supported `token.network` selector in `type` overrides explicit token and network fields and their admin defaults. An unknown nonempty type returns `10009` (for example `usdt-tron`). With an empty type or `alipay`, each missing field falls back to its respective EPay default. If both are still empty, the server creates a placeholder. If exactly one is missing after defaults, it rejects the request. Currency uses the request value, then the configured default, then `cny`.

Fresh installations seed `cny` and leave default token and network empty. Existing settings survive upgrades. The server sets `payment_type=Epay` after signature verification. Sign any `type`, `token`, `network` and `currency` you actually send.

<a id="callbacks"></a>

## Merchant callbacks

EPUSDT notifies the order’s `notify_url` after successful payment. Verify the signature, match the order and amount, and process repeated notifications idempotently. Acknowledge with HTTP 200 and a body of `ok` or `success` (case-insensitive). Failed delivery is retried according to `order_notice_max_retry` and `callback_retry_base_seconds`, with exponential backoff capped at five minutes.

### GMPay

GMPay sends JSON via POST. Look up the local merchant key by `pid` and verify the HMAC using the same canonicalization as order creation. The body omits `payment_type`.

```json
{
  "pid": "1000",
  "trade_id": "20260523171652123456001",
  "order_id": "ORD202605230001",
  "amount": 100,
  "actual_amount": 14.29,
  "receive_address": "TTestTronAddress001",
  "token": "USDT",
  "block_transaction_id": "0xabc123...",
  "signature": "498975a97bc34563bdb14df53fc18054645df9684d6c67d9b9dd90ec62be1018",
  "status": 2
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `pid` | string | Merchant signing-key ID. |
| `trade_id` | string | EPUSDT transaction ID. |
| `order_id` | string | Merchant order ID. |
| `amount` | number | Fiat amount. |
| `actual_amount` | number | Crypto amount received. |
| `receive_address` | string | Receiving address. |
| `token` | string | Asset received. |
| `block_transaction_id` | string | On-chain hash or provider order reference. |
| `signature` | string | Lowercase HMAC-SHA256 digest. |
| `status` | integer | Paid (2). |

### EPay

EPay sends a GET request with a numeric `pid`. Use a numeric merchant PID for EPay and for GMPay orders selecting `payment_type=Epay`. The callback uses the order’s saved `type` or falls back to `alipay`. Verify the MD5 signature as for an incoming EPay order.

```text
pid=1000
trade_no=20260523171652123456001
out_trade_no=ORD202605230001
type=alipay
name=VIP
money=100.0000
trade_status=TRADE_SUCCESS
sign=cc44fab3f264615d859f8a1fd36d2f08
sign_type=MD5
```

## OKPay provider callback

Configure `POST /payments/okpay/v1/notify` in OKPay. It accepts JSON, URL-encoded form, multipart form or a raw query-string body. EPUSDT verifies the provider signature with the shop token, settles the corresponding order and triggers the merchant callback. This covers a completed placeholder or a hosted child order.

Successful response (plain text):

```text
success
```

Failure (HTTP 400):

```text
fail
```

## Application error codes

| Code | HTTP | Meaning |
| --- | --- | --- |
| `200` | 200 | Success |
| `400` | 400 | System or validation error |
| `401` | 401 | Invalid signature |
| `10001` | 400 | Wallet address already exists |
| `10002` | 400 | Order already exists |
| `10003` | 400 | No available receiving address |
| `10004` | 400 | Amount below payment precision |
| `10005` | 400 | No available address/amount combination |
| `10006` | 400 | Exchange-rate calculation failed |
| `10007` | 400 | Block already processed |
| `10008` | 400 | Order not found |
| `10009` | 400 | Invalid parameters |
| `10010` | 400 | Order state changed |
| `10011` | 400 | Child order limit reached |
| `10012` | 400 | Cannot switch networks on a child order |
| `10013` | 400 | Order is not pending |
| `10014` | 400 | Network disabled |
| `10016` | 400 | Asset not found |
| `10017` | 400 | Payment provider disabled |
| `10018` | 400 | Payment provider configuration incomplete |
| `10019` | 400 | Unsupported provider token or network |
| `10038` | 400 | Transaction hash verification failed |
| `10039` | 400 | Manual verification requires an on-chain order |
| `10041` | 400 | Invalid notification URL |
| `10042` | 400 | Provider order creation failed |
| `10044` | 400 | Invalid redirect URL |
| `10045` | 400 | Order merchant key unavailable |
| `10046` | 400 | Unable to sign EPay return parameters |

[Full application error list](../src/util/constant/errno.go)
