# Epusdt API 文档

开发者可通过 Epusdt 提供的 HTTP API 将收款能力集成到业务系统。本文档以当前代码路由为准。

> 旧版 `POST /api/v1/order/create-transaction` 已不再注册；创建订单请使用 `POST /payments/gmpay/v1/order/create-transaction`。

## 接口总览

| 场景 | 方法 | 路径 | 是否需要签名 |
| --- | --- | --- | --- |
| 创建 GMPay 交易 | POST | `/payments/gmpay/v1/order/create-transaction` | 是 |
| 获取公开支付配置 | GET | `/payments/gmpay/v1/config` | 否 |
| 收银台页面 | GET | `/pay/checkout-counter/{trade_id}` | 否 |
| 收银台初始化数据 | GET | `/pay/checkout-counter-resp/{trade_id}` | 否 |
| 查询支付状态 | GET | `/pay/check-status/{trade_id}` | 否 |
| 切换支付网络/通道 | POST | `/pay/switch-network` | 否 |
| EPay 兼容创建交易 | GET/POST | `/payments/epay/v1/order/create-transaction/submit.php` | 是 |
| OkPay 平台回调 | POST | `/payments/okpay/v1/notify` | OkPay 签名 |

## 统一响应格式

除重定向和纯文本回调接口外，接口返回 JSON：

```json
{
  "status_code": 200,
  "message": "success",
  "data": {},
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `status_code` | integer | 业务状态码。成功为 `200`，错误码见文末。 |
| `message` | string | 返回消息。 |
| `data` | object/null | 接口数据。 |
| `request_id` | string | 请求 ID，服务端自动生成。 |

签名错误会返回 HTTP 401；业务错误通常返回 HTTP 400，并在 `status_code` 中给出具体业务码。

## 签名规则

当前版本使用统一商户凭证。请求必须携带 `pid`，服务端用 `pid` 查询对应的 `secret_key` 作为签名密钥。默认安装会创建一个 PID 为 `1000` 的默认密钥。

### GMPay 签名

1. 将所有非空参数按参数名 ASCII 字典序升序排序。
2. 使用 `key=value` 形式以 `&` 拼接。
3. 不参与签名的字段：`signature`。
4. 使用 `secret_key` 作为 HMAC 密钥，对拼接字符串计算 HMAC-SHA256。
5. 将结果编码为 64 位小写十六进制字符串，作为 `signature`。

> GMPay 已硬切至 HMAC-SHA256，不再接受旧版 MD5 签名，也不提供算法协商或回退。

注意：

- `pid` 必须参与签名。
- GMPay 的 `payment_type` 不是必填；如果请求里传了非空 `payment_type`，它和其他非空参数一样必须参与签名。
- 空字符串和 `null` 不参与签名。
- 参数名区分大小写。
- JSON 数字会按服务端数字格式参与签名，例如 `100.00` 会被解析为 `100`；如果需要保留字符串格式，可使用 `application/x-www-form-urlencoded`。

示例参数：

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

以下示例假设 `secret_key` 为 `epusdt_secret_key`，仅用于演示签名计算。

待签名字符串：

```text
amount=100&currency=cny&name=VIP&network=tron&notify_url=https://merchant.example/notify&order_id=ORD202605230001&pid=1000&redirect_url=https://merchant.example/return&token=usdt
```

得到：

```text
signature=6f874b1919d95081835e2809b620e354a5866f5a6dbb2e432d1627f1eb10059d
```

### PHP 签名示例

GMPay 使用 `signature` 字段，签名时只排除 `signature`：

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

EPay 兼容接口使用 `sign` 字段，签名时排除 `sign` 和 `sign_type`：

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

## 创建 GMPay 交易

`POST /payments/gmpay/v1/order/create-transaction`

支持：

- `Content-Type: application/json`
- `Content-Type: application/x-www-form-urlencoded`

### 请求示例

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

### 请求参数

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `pid` | string | 是 | 商户 PID，用于查找 API Key，并参与签名。 |
| `order_id` | string | 是 | 商户订单号，最长 32 字符，不能重复。 |
| `currency` | string | 是 | 法币币种，如 `cny`、`usd`。 |
| `token` | string | 条件必填 | 收款币种，如 `usdt`、`trx`、`usdc`、`sol`。GMPay 可与 `network` 同时省略以创建状态 `4` 占位订单。 |
| `network` | string | 条件必填 | 收款网络，如 `tron`、`solana`、`ethereum`、`bsc`、`polygon`、`plasma`。GMPay 可与 `token` 同时省略以创建状态 `4` 占位订单。 |
| `amount` | number | 是 | 法币金额，必须大于 `0.01`。 |
| `notify_url` | string | 是 | 支付成功异步回调地址。 |
| `redirect_url` | string | 否 | 支付完成后的同步跳转地址。 |
| `name` | string | 否 | 商品/订单名称。 |
| `payment_type` | string | 否 | GMPay 兼容字段，不要求必须传；如果传了非空值，必须参与 GMPay `signature` 计算。普通 GMPay 不传时后台会存为 `Gmpay`；传 `Epay`（大小写不敏感）会统一存为 `Epay` 并使用 EPay 回调格式，且 PID 必须是数字。 |
| `signature` | string | 是 | 64 位小写十六进制 GMPay HMAC-SHA256 签名。 |

`token` 和 `network` 必须同传或同缺。两者同缺时只创建包含 `amount/currency` 的占位订单，状态为 `4`，不会分配钱包、不会计算链上支付金额，也不会锁定交易金额；后续由收银台调用 `/pay/switch-network` 选择具体链和币种或 OkPay。只缺其中一个会返回参数错误。

建议先调用 `/payments/gmpay/v1/config` 获取可用的 `network` 和 `token` 组合。

### 成功响应

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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `trade_id` | string | Epusdt 交易号。 |
| `order_id` | string | 商户订单号。 |
| `amount` | number | 商户提交的法币金额。 |
| `currency` | string | 法币币种。 |
| `actual_amount` | number | 实际需支付的加密货币数量。 |
| `receive_address` | string | 收款地址。 |
| `token` | string | 收款币种。 |
| `status` | integer | 订单状态。状态 `4` 表示等待用户选择 `token/network`。 |
| `expiration_time` | integer | 订单过期时间，秒级时间戳。 |
| `payment_url` | string | 收银台地址。该地址会跳转到前端收银台。 |

状态 `4` 占位订单的 `actual_amount` 为 `0`，`receive_address` 和 `token` 为空；过期任务或后台关闭只会把它改为状态 `3`，不会执行交易金额解锁。第一次成功调用 `/pay/switch-network` 时，如果选择普通链上 `token/network`，同一个父订单会原地补全链上字段并变为状态 `1`，此时才会创建真实交易锁；如果选择 `network=okpay`，同一个父订单会原地变为 OkPay 订单并返回 OkPay 托管支付链接，不创建子订单，也不会分配本系统钱包地址或链上锁。占位父单首次补全后 `is_selected` 仍为 `false`，后续同目标选择才会把父单标记为已选中；如果后续切到其它支付目标，则创建唯一一条子订单。

## 获取公开支付配置

`GET /payments/gmpay/v1/config`

返回收银台展示配置、可用链/币种、EPay 默认配置和 OkPay 公共配置。

### 成功响应示例

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
      "background_image_url": "https://cdn.example.com/background.png"
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
    "version": "v1.0.1"
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```

`supported_assets` 只包含同时满足以下条件的组合：

- 链已启用。
- 该链有可用钱包地址。
- 该链至少有一个启用中的 token。

## 收银台页面

`GET /pay/checkout-counter/{trade_id}`

用于浏览器打开收银台。当前实现会返回 301，并跳转到：

```text
/cashier/{trade_id}
```

创建交易接口返回的 `payment_url` 即为该地址。

## 收银台初始化数据

`GET /pay/checkout-counter-resp/{trade_id}`

用于前端收银台读取订单展示数据。该接口只确认订单存在并返回基础数据；当前支付状态请调用 `/pay/check-status/{trade_id}`。

### 成功响应示例

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

注意：该接口的 `expiration_time` 和 `created_at` 是毫秒级时间戳。

如果订单是状态 `4` 占位订单，返回的仍是同一个父订单 `trade_id`，但链上支付字段尚未生成。该状态可能来自 GMPay 空 token/network 创建，也可能来自 EPay submit.php 在请求和数据库默认值都没有完整 token/network 时创建：

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

`payment_type` 是归一化后的接入类型：底层订单存储为 `Epay/Gmpay`，该接口转为小写 `epay/gmpay` 返回；`epay` 会走 EPay 回调格式，`gmpay` 走默认 GMPay JSON 回调格式。

前端看到 `status=4` 时，应展示选择网络和币种/支付通道的界面，并在用户选择后调用 `/pay/switch-network`。选择链上支付成功后，该父订单会变为 `status=1`，`actual_amount`、`token`、`network`、`receive_address` 会被补全，但 `is_selected` 保持 `false`，由后续同目标选择流程标记为已选中。选择 OkPay 成功后，接口返回同一个父订单 `trade_id` 和第三方 `payment_url`；父订单会变为 `status=1`、`is_selected=false`、`pay_provider=okpay`、`network=okpay`、`receive_address=OKPAY`。

## 查询支付状态

`GET /pay/check-status/{trade_id}`

### 成功响应示例

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

订单状态：

| 值 | 说明 |
| --- | --- |
| `1` | 等待支付 |
| `2` | 支付成功 |
| `3` | 已过期 |
| `4` | 等待选择支付网络/币种 |

## 切换支付网络/通道

`POST /pay/switch-network`

该接口通常由收银台前端调用，用于切换到另一个链上收款地址，或切换到 OkPay 托管收银台。

### 请求示例

```json
{
  "trade_id": "20260523171652123456001",
  "token": "USDT",
  "network": "solana"
}
```

切换到 OkPay：

```json
{
  "trade_id": "20260523171652123456001",
  "token": "USDT",
  "network": "okpay"
}
```

### 请求参数

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `trade_id` | string | 是 | 父订单交易号。 |
| `token` | string | 是 | 目标币种。 |
| `network` | string | 是 | 目标网络，或特殊值 `okpay`。 |

### 成功响应

返回结构与收银台初始化数据一致。链上订单的 `payment_url` 为空；OkPay 订单的 `payment_url` 是 OkPay 返回的托管支付链接。若父订单仍是 `status=4`，首次切换链上或 OkPay 都会原地补全父订单并返回同一个 `trade_id`。

说明：

- 只能对父订单切换网络，不能对子订单继续切换。
- 父订单必须处于等待支付状态 `1`，或占位状态 `4`。
- 状态 `4` 第一次选择具体链和币种时，会原地补全父订单并返回同一个 `trade_id`，不会创建子订单。
- 状态 `4` 第一次选择 `network=okpay` 时，不要求父订单已有链上字段；系统会原地把父订单补成 OkPay 订单并返回同一个 `trade_id` 与 OkPay `payment_url`，不会创建子订单。
- 状态 `4` 补全后订单变为状态 `1`，但 `is_selected` 保持 `false`；之后同目标选择会返回父单并标记选中，切到其它支付目标才创建子订单。
- 每个父订单最多创建 1 个子订单；已经创建过子订单后，不能再用该父单创建第二个新子订单。子订单本身不能继续切换网络。
- 如果切换到同一组 `token + network`，会返回已有订单。

## EPay 兼容创建交易

`GET /payments/epay/v1/order/create-transaction/submit.php`

`POST /payments/epay/v1/order/create-transaction/submit.php`

该接口兼容传统 EPay/易支付接入方式。成功后不会返回 JSON，而是 HTTP 302 跳转到：

```text
/pay/checkout-counter/{trade_id}
```

### 请求参数

| 字段 | 位置 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `pid` | query/form | string | 是 | 商户 PID。建议使用数字 PID；EPay 回调会按数字 PID 输出。 |
| `money` | query/form | number | 是 | 法币金额。 |
| `out_trade_no` | query/form | string | 是 | 商户订单号。 |
| `notify_url` | query/form | string | 是 | 异步回调地址。 |
| `return_url` | query/form | string | 否 | 支付完成后的同步跳转地址。 |
| `name` | query/form | string | 否 | 商品/订单名称。 |
| `type` | query/form | string | 否 | 仅支持空值、`alipay`，或当前已启用并可收款的 `token.network` selector（如 `usdt.tron`）。推荐使用小写 `alipay`。 |
| `token` | query/form | string | 否 | 可选收款币种。仅在 `type` 不是命中的 selector 时参与解析；传了就必须参与 EPay 签名。 |
| `network` | query/form | string | 否 | 可选收款网络。仅在 `type` 不是命中的 selector 时参与解析；传了就必须参与 EPay 签名。 |
| `currency` | query/form | string | 否 | 可选法币币种。优先级高于后台 `epay.default_currency`；传了就必须参与 EPay 签名。 |
| `sign` | query/form | string | 是 | EPay 签名。 |
| `sign_type` | query/form | string | 否 | 通常为 `MD5`。 |

签名规则：

- 使用 `pid` 对应的 `secret_key`。
- 排除 `sign` 和 `sign_type`。
- 其他非空参数按 ASCII 字典序拼接后追加 `secret_key` 并 MD5；如果接入插件额外传了 `sitename` 等字段，也要一起参与签名。

示例待签名字符串：

```text
money=100&name=VIP&notify_url=https://merchant.example/notify&out_trade_no=ORD202605230001&pid=1000&return_url=https://merchant.example/return&type=alipayepusdt_secret_key
```

得到：

```text
sign=b865b0acbb2b01554c35a1bd33351452
```

EPay 接口解析 `type/token/network/currency` 的规则：

- `type` 只接受三类输入：空值、`alipay`、命中的 `token.network` selector。
- `type=token.network` 且命中当前已启用支付资产时，会直接确定本次订单的 `token/network`，并覆盖请求参数里的 `token/network` 以及后台 `epay.default_token` / `epay.default_network`。
- `type` 非空但既不是命中的 selector，也不是 `alipay` 时，直接返回 `10009 invalid params`。例如 `usdt-tron`、未启用的 `usdc.tron` 都会被拒绝。
- `type` 为空或为 `alipay` 时，`token/network` 继续走原有解析：先看请求参数，再分别用数据库 `epay.default_token` / `epay.default_network` 补齐。
- `currency` 解析不受 selector 影响：请求参数 `currency` > 数据库 `epay.default_currency` > `cny`。
- 最终解析结果里，`token/network` 同时有值时创建具体链上订单；同时为空时创建状态 `4` 占位订单；最终只缺一个时返回参数错误。
- 这意味着“请求里只传了一个值”不一定报错；如果另一个值能被 default 补齐，仍会成功。只有最终解析后仍然只剩一个值，才返回 `10009`。
- 服务端会在 EPay 签名校验通过后内部注入 `payment_type=Epay`，该字段不参与 EPay 入站签名；但请求里显式传入的 `type/token/network/currency` 仍属于原始 EPay 参数，必须参与签名。

后台默认配置可通过 `/payments/gmpay/v1/config` 的 `epay` 字段查看；新安装默认只预置 `epay.default_currency=cny`，`epay.default_token` 和 `epay.default_network` 为空，因此 EPay 未显式传 token/network 时会创建状态 `4` 占位订单。已有数据库的配置不会被 seed 覆盖，删除或置空 `epay.default_token` 和 `epay.default_network` 后，这两个字段会返回空字符串。

## 商户异步回调

订单支付成功后，Epusdt 会向订单的 `notify_url` 发送异步通知。目标服务器处理完成后需返回 HTTP 200，响应体为 `ok` 或 `success`（大小写不敏感）。否则会按队列配置重试：首次失败后最多重试 `order_notice_max_retry` 次，重试间隔按 `callback_retry_base_seconds` 指数退避，最大 5 分钟。

### GMPay 回调

普通 GMPay 订单使用 POST JSON 回调。

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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `pid` | string | 订单所属 API Key 的 PID。商户应使用该 PID 查本地密钥验签。 |
| `trade_id` | string | Epusdt 交易号。 |
| `order_id` | string | 商户订单号。 |
| `amount` | number | 商户提交的法币金额。 |
| `actual_amount` | number | 实际到账的加密货币数量。 |
| `receive_address` | string | 收款地址。 |
| `token` | string | 收款币种。 |
| `block_transaction_id` | string | 链上交易哈希或第三方支付订单号。 |
| `signature` | string | 64 位小写十六进制 HMAC-SHA256 回调签名。 |
| `status` | integer | 当前仅支付成功时回调，值为 `2`。 |

GMPay 回调验签方式与创建订单一致：排除 `signature` 字段后，使用商户 `secret_key` 对规范化参数字符串计算 HMAC-SHA256。回调体不包含 `payment_type` 字段。

### EPay 兼容回调

通过 EPay 兼容接口创建的订单，会使用 GET 请求回调 `notify_url`，参数如下：

> EPay 回调会把 `pid` 输出为数字；使用 EPay 兼容接口或 `payment_type=Epay` 时，请确保 API Key 的 PID 是数字。
>
> `type` 出站时使用订单里保存的请求值。当前主分支正常入站能保存下来的只会是 `alipay` 或命中的 `token.network` selector；如果入站请求没传 `type`，出站才回退为 `alipay`。

```text
pid=1000
trade_no=20260523171652123456001
out_trade_no=ORD202605230001
type=alipay
name=VIP
money=100.0000
trade_status=TRADE_SUCCESS
sign=a1b2c3d4...
sign_type=MD5
```

验签时排除 `sign` 和 `sign_type`，其余非空参数按 ASCII 字典序拼接后追加 `secret_key` 并 MD5。

## OkPay 平台回调

`POST /payments/okpay/v1/notify`

这是 OkPay/OkayPay 平台通知 Epusdt 的接口，不是商户系统主动调用的接口。配置 OkPay 时，回调地址应填写该路径。

支持 JSON、`application/x-www-form-urlencoded`、multipart form 和原始 query-string 风格 body。成功返回纯文本：

```text
success
```

失败返回 HTTP 400：

```text
fail
```

Epusdt 会按配置的 OkPay shop token 验证 OkPay 签名，成功后将对应 OkPay 订单标记为已支付，并触发商户回调；这个 OkPay 订单可能是由 `status=4` 占位父单原地补全而来，也可能是后续切换创建的子订单。

## status_code 返回状态码及含义

| 状态码 | HTTP 状态 | 说明 |
| --- | --- | --- |
| `200` | 200 | 成功 |
| `400` | 400 | 系统错误，或普通参数/验证错误 |
| `401` | 401 | 签名认证错误 |
| `10001` | 400 | 钱包地址已存在 |
| `10002` | 400 | 支付交易已存在，请勿重复创建 |
| `10003` | 400 | 无可用钱包地址，无法发起支付 |
| `10004` | 400 | 支付金额有误，无法满足最小支付单位 |
| `10005` | 400 | 无可用金额通道 |
| `10006` | 400 | 汇率计算错误 |
| `10007` | 400 | 订单区块已处理 |
| `10008` | 400 | 订单不存在 |
| `10009` | 400 | 无法解析参数 |
| `10010` | 400 | 订单状态已变化 |
| `10011` | 400 | 超过子订单数量上限 |
| `10012` | 400 | 不能对子订单切换网络 |
| `10013` | 400 | 订单不是等待支付状态 |
| `10014` | 400 | 链未启用 |
| `10016` | 400 | 支持的资产不存在 |
| `10017` | 400 | 支付服务商未启用 |
| `10018` | 400 | 支付服务商配置不完整 |
| `10019` | 400 | 支付服务商不支持该币种或网络 |
