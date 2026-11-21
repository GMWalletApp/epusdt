# X Layer (OKX Onchain OS Payment) — exploration map

Read-only map of how the OKPay hosted-checkout provider works in this repo, so X Layer can mirror it. Base
path below is `src/` under `~/workspace/goals/polygon/files/epusdt-source/`. No code was changed. Frontend
source is NOT in this repo: `src/www/` holds only the built SPA (`index.html` + `www/assets/*.js`, 9.2M); no
`*.tsx`/`*.ts`/`package.json` anywhere in the repo. Admin + cashier UI behaviour is therefore only observable
in minified built JS.

## 0. What X Layer needs (from `~/workspace/user/files/onchainos-payment-api-20261116.pdf`)

- Base `https://web3.okx.com`, prefix `/api/v6/pay/onchainos-payment`; `POST /create` returns
  `paymentIntentId` + `checkoutUrl`; `POST /query` (by `paymentIntentId` XOR `merchantPaymentId`) returns
  `status`.
- Auth on every call: `OK-ACCESS-KEY`, `OK-ACCESS-SIGN`, `OK-ACCESS-PASSPHRASE`, `OK-ACCESS-TIMESTAMP`; sign =
  method+path+body per the existing OnchainOS API-Key spec. **No webhook** — poll `/query` until
  `status=settled`; "页面跳转和 txHash 均不能替代最终状态".
- Statuses: `pending` → `settled` (success); `paid` = paid, not yet settled (keep polling); `expired`,
  `failed` (terminal).
- `create` body: `merchantPaymentId` (≤64 chars, unique per API key), `to` (merchant receiving EVM address),
  `fiatAmount`/`fiatCurrency` (reference only), `quotes[0].paymentToken` = `{chainId:"196", tokenAddress,
  tokenAmount}` (smallest-unit string, ≥1000000 = 1 token), optional `returnUrl`, `merchantDisplayName`,
  `productDesc`, `expireSeconds` (1–1800, default 1800). Tokens (chainId `"196"`, all 6 decimals): USDC
  `0xb6ceceab302e2e4948951ee7843fc24e92933061`, USD₮0 `0x779ded0c9e1022225f8e0630b35a9b54be713736`, USDG
  `0x4ae46a509f6b1d9056937ba4500cb143933d2dc8`. Minimum order = 1 token.
- Key difference from OKPay: OKPay is form-POST + MD5 sign + server webhook (`/payments/okpay/v1/notify`); X
  Layer is JSON + OK-ACCESS headers + **polling only**, so a new poll job is required and there is no notify
  controller to copy.

## 1. Settings — OKPay as template

Storage: generic `settings` table, `model/mdb/settings.go:16,58-65`: `SettingGroupOkPay = "okpay"` and 8 keys
— `okpay.enabled` (bool), `okpay.shop_id`, `okpay.shop_token`, `okpay.api_url`, `okpay.callback_url`,
`okpay.return_url`, `okpay.timeout_seconds` (int), `okpay.allow_tokens` (`"USDT,TRX"`). Seeded defaults in
`model/dao/mdb_table_init.go:170-188` (`seedDefaultSettings`, `ON CONFLICT DO NOTHING`): enabled=`false`,
api_url=`https://api.okaypay.me/shop/`, callback_url= `<app_uri>/payments/okpay/v1/notify`, timeout=`10`,
allow_tokens= `USDT,TRX`. `shop_id`/`shop_token`/`return_url` have **no** seed rows — getters fall back to
`""`. Getters in `model/data/settings_data.go:226-280`, e.g. `GetOkPayEnabled()` → `GetSettingBool(…Enabled,
false)`, `GetOkPayAPIURL()` → default `https://api.okaypay.me/shop/`, `GetOkPayCallbackURL()` → configured
value else `appURI + "/payments/okpay/v1/notify"`. Settings are cached in-memory (`loadAllSettings`,
`:25-44`); `SetSetting` (`:152-171`) upserts by key — new keys need no cache change. Admin API,
`controller/admin/settings_controller.go`: `GET /admin/api/v1/settings?group=okpay` → `ListSettings` (:88)
returns raw `[]mdb.Setting` rows via `data.ListSettingsByGroup` — **not schema-driven**: the backend returns
flat rows, the frontend decides which fields exist. `PUT /admin/api/v1/settings` → `UpsertSettings` (:116),
body `{items:[{group,key,value,type}]}`; per-item validation in `normalizeAndValidateSettingItem` (:223) only
special-cases rate/system keys — okpay keys pass through unvalidated; items save independently, errors
returned per key. The `enums:"brand,rate,system,epay,okpay"` on `SettingUpsertItem` (:65) and doc comments
(:38-46, :102-104) are the only backend places naming the `okpay` group. Admin UI is hardcoded in built JS:
`www/assets/okpay-DzwNhqlx.js` (route `/settings/okpay`; `router-aZWjwYfd.js`:
`AuthenticatedSettingsOkpayRoute`, nav in `route-BkjShyH8.js`). It renders exactly the 8 keys above
(`H(`okpay`)` settings query; `G(t,`okpay.enabled`)`, `…shop_id`, `…shop_token`, `…api_url`, `…callback_url`,
`…return_url`, `…timeout_seconds`, `…allow_tokens`; PUTs
`{group:`okpay`,key:`okpay.shop_token`,type:`string`,…}`). A hand-written form per provider, not generated
from backend group/keys. **A new `xlayer` admin page cannot be produced by backend changes alone.**

## 2. Cashier — how it learns about OKPay

Public config: `GET /payments/gmpay/v1/config` (`controller/comm/supported_asset_controller.go:88`,
`GetPublicConfig`), also `GET /admin/api/v1/config` (same handler; admin identity adds credentials). Response
`model/response/support_response.go:24-41`: `OkPayPublicConfig{enabled, allow_tokens, shop_id?, shop_token?,
api_url?, callback_url?, return_url?, timeout_seconds?}` (credentials `omitempty`) in
`PublicConfigResponse{supported_assets, site, epay, okpay, version}`. Public callers get only `enabled` +
`allow_tokens` (`:93-103`); credentials are filled only when `ctx.Get(middleware.AdminUserIDKey) != nil`.
Cashier behaviour (built JS `www/assets/_trade_id-B2s-YYlo.js`, route `/pay/checkout-counter/:trade_id`,
`route/router.go:62`): loads order via `GET /pay/checkout-counter-resp/{trade_id}`, config via `GET
/payments/gmpay/v1/config`, polls via `GET /pay/check-status/{trade_id}`. OKPay is offered iff
`O?.okpay?.enabled && ne.length`, where `ne = okpay.allow_tokens`; token radio ids are
`okpay-token-<lowercase>`; a single-option auto-select exists (`ae===1&&(oe=re?`chain`:`okpay`)`). Switching:
`POST /pay/switch-network` with `{trade_id, token, network:"okpay"}` (`model/request/order_request.go:59-63`).
Provider vs on-chain rendering is keyed off the response: `c===`okpay` || network===`okpay` || (payment_url &&
!receive_address)` → hosted panel, else address + tx-hash panel. On hosted confirm it opens a popup:
`window.open(C,`okpay_checkout`,`popup,width=480,height=720`)` where `C = i?.payment_url`. `payment_url` is
`CheckoutCounterResponse.PaymentUrl` (`model/response/pay_response.go:15`, json `payment_url`): "链上订单为空，OkPay
订单为第三方 payLink". `submit-tx-hash` is explicitly unsupported for provider orders
(`controller/comm/pay_controller.go:83`).

## 3. Order flow — OKPay end to end

Entry: `POST /pay/switch-network` (`controller/comm/order_controller.go:80` → `service.SwitchNetwork`,
`model/service/order_service.go:568`). `network=="okpay"` branches at `:590` to `switchToOkPay` (:861);
anything else is on-chain. `switchToOkPay` (:861-1039) — two paths, same shape: (1) Parent is `status=4`
placeholder → `completeWaitSelectOkPayOrder` (:783): rate-convert fiat→token (`config.GetRateForCoin(token,
currency)`), insert a `provider_orders` row with status `creating`, call the provider, then in a second tx
store provider id + pay URL and complete the parent **in place** via
`data.CompleteWaitSelectOkPayOrderWithTransaction` (parent becomes `status=1`, `network="okpay"`,
`receive_address="OKPAY"`, `pay_provider="okpay"`; `model/data/order_data.go:354-365`). (2) Parent already
concrete → create one sub-order (`MaxSubOrders = 1`, `:566`) with `ReceiveAddress:"OKPAY"`, `Network:
mdb.NetworkTron` (dummy value, unused for provider rows — `:959`), `PayProvider:"okpay"`, `Status:1`, plus a
`creating` provider row; then the same provider call + finalize tx. Re-selection reuses the stored `PayURL`
instead of re-creating. Provider call: `createOkPayDepositOrder` (`model/service/okpay_service.go:73-131`):
form-POST `{unique_id, name, amount, coin, callback_url, return_url}` + MD5 sign `okPaySign` (:50, `md5(query
+ "&token=" + shopToken)`, upper-hex) to `<api_url>/payLink`; response `{status, code, data:{order_id,
pay_url}, msg}` → `{ProviderOrderID, PayURL}`. Failure → `MarkProviderOrderFailed` +
`PaymentProviderCreateErr` (10042); config problems → `PaymentProviderNotEnabled` (10017)
/`PaymentProviderConfigErr` (10018) / `PaymentProviderNotSupport` (10019) (`util/constant/*:73-75,98`).
Storage: `model/mdb/provider_order.go` — table `provider_orders`, unique on `(trade_id, provider)`; fields
`trade_id, provider ("okpay"), provider_order_id, pay_url (512), amount, coin, status, notify_raw`. Statuses
(`:1-6`): `creating / pending / paid / failed / expired`. Helpers in `model/data/provider_order_data.go:10-86`
(get/create/upsert- creating/update-created/mark failed|paid|expired/save-notify). Order rows:
`model/mdb/orders_mdb.go:1-15,28-35` — `Status`: 1 waiting, 2 paid, 3 expired, 4 wait-select;
`PaymentProviderOkPay = "okpay"`, `PaymentProviderOnChain = "on_chain"`; `Orders.PayProvider` is the per-row
settlement flag. Confirmation today is webhook-only — **no task polls provider orders**: `task/listen.go:8
Start()` launches only chain listeners + `ListenSolJob` (@every 5s) + `RpcHealthJob` (@every 30s); no
`ProviderOrder`/`OkPay` reference exists in `task/` or `task_service.go`. Notify path: `POST
/payments/okpay/v1/notify` (`route/router.go:87-88` → `controller/comm/okpay_controller.go:32 OkPayNotify`,
form/JSON flattening `:96-145`) → `service.HandleOkPayNotify` (`okpay_service.go:206-283`): verify MD5
signature → check shop id, `unique_id`/`order_id`, order `PayProvider=="okpay"`, provider-row id + coin +
amount match → require `status=="success"`, `notify_type=="deposit"`, `pay_status=="1"` → `OrderProcessing`
(`order_service.go:243`, sets status 2, dedupes on `block_transaction_id`, expires sibling sub-orders) →
`sendPaymentNotification` (`task_service.go:338`, `notify.Dispatch(mdb.NotifyEventPaySuccess, msg)`,
`notify/dispatcher.go:43`) → `data.MarkProviderOrderPaid`. The provider order id is passed as
`BlockTransactionId` (the OKPay `order_id`). `GetCheckoutCounterByTradeId` (`pay_service.go:24-33`)
re-attaches `providerRow.PayURL` to `resp.PaymentUrl` when `PayProvider=="okpay"`.

## 4. Chains / tokens — can "xlayer" be a chain?

- Chains: `model/data/chain_data.go:18 ListEnabledChains()` (rows with `enabled=true`); seeds in
  `model/dao/mdb_table_init.go:67-75`: tron, ethereum, solana, binance, polygon, plasma, ton, aptos — **no
  xlayer row**. Network constants in `model/mdb/wallet_address_mdb.go:9-16`. Tokens:
  `model/data/chain_token_data.go:12,35`; seeds `:92-114` (USDT/USDC per chain with contract + decimals).
  `buildSupportedAssets` (§2) only lists a network if it has a wallet address **and** ≥1 enabled token passing
  `ChainTokenReadyForPayment` (`model/service/payment_asset.go:9-17`; `chainTokenRequiresAssetID` returns
  `true` by default for unknown networks, so X Layer tokens with contract addresses would pass). Adding
  `xlayer` as a chain + USDC/USD₮0/USDG tokens would make it appear in `supported_assets` as an **on-chain**
  option — the wrong shape. (OKPay tokens bypass this entirely: they come from `okpay.allow_tokens` in public
  config, not from chains.)
- Every seeded chain has a dedicated listener in `task/listen.go` (EVM websocket + backfill scanners,
  Tron/Ton/Aptos block scanners, `ListenSolJob`). There is **no generic/EVM listener** — an `xlayer` chain row
  with no listener would silently never confirm on-chain payments, and a wallet address would be needed for it
  to be listed at all. OKPay avoids this entirely: it is **not** a chain, it is a `pay_provider` +
  `network="okpay"` pseudo-network selected via `switch-network`, with no chain row, no tokens, no wallet, no
  listener. X Layer must follow the OKPay pattern, not the chain pattern.

## 5. Conclusion — minimal backend change surface (mirror OKPay, in order; backend-only, all in Go, no DB migration — `provider_orders` and `settings` are generic; `settings` rows are upsert-by-key):
1. `model/mdb/settings.go` — add `SettingGroupXLayer = "xlayer"` and `SettingKeyXLayer*` constants. Suggested
   keys (from §0): `xlayer.enabled` (bool), `xlayer.api_key`, `xlayer.api_secret`, `xlayer.passphrase`,
   `xlayer.api_url` (default `https://web3.okx.com`), `xlayer.return_url`, `xlayer.timeout_seconds`,
   `xlayer.allow_tokens` (`USDC,USD₮0,USDG`), `xlayer.to_address` (OKPay has no equivalent; `create` requires
   `to`). Note: OKPay's `shop_token` is returned to admin via the `GetPublicConfig` admin variant — make the
   same exposure decision deliberately for X Layer credentials.
2. `model/dao/mdb_table_init.go:seedDefaultSettings` — seed `xlayer.enabled=false`, `api_url`,
   `timeout_seconds`, `allow_tokens`; no seed for key/secret/passphrase/to_address.
3. `model/data/settings_data.go` — `GetXLayer*()` getters mirroring `GetOkPay*()` (:226-280), incl.
   allow-tokens parsing.
4. `controller/admin/settings_controller.go` — add `xlayer` to the `enums` + doc comments (:38-46, :65,
   :102-104). `UpsertSettings` /`ListSettings` need no logic change (generic by group/key).
5. `model/response/support_response.go` — add `XLayerPublicConfig` (`enabled`, `allow_tokens`, admin-only
   credentials) and an `XLayer` field on `PublicConfigResponse`;
   `controller/comm/supported_asset_controller.go:88-124` (`GetPublicConfig`) to fill it like `OkPay`.
6. `model/mdb/orders_mdb.go` — add `PaymentProviderXLayer = "xlayer"`.
7. `model/service/xlayer_service.go` (new, clone of `okpay_service.go`) — JSON `create` (fiat→token via
   `config.GetRateForCoin`, `tokenAmount` in smallest units as a string, min 1 token), OK-ACCESS header
   signing, `query` by `paymentIntentId`, status mapping (`settled` = success; `paid` = keep polling;
   `expired`/`failed` = terminal). No notify handler (no webhook).
8. `model/service/order_service.go` — `SwitchNetwork` (:568-591): add `network == "xlayer"` branch; clone
   `switchToOkPay` (:861) + `completeWaitSelectOkPayOrder` (:783) as `switchToXLayer`
   /`completeWaitSelectXLayerOrder`, and `xlayerTokenAllowed` (≈:1032). Reuse `provider_orders` +
   `OrderProcessing` + `sendPaymentNotification`.
9. `model/service/pay_service.go:24-33` — re-attach stored `PayURL` for `PayProvider=="xlayer"` in
   `GetCheckoutCounterByTradeId`.
10. `task/` + `model/service/task_service.go` — **new**: a poll job (e.g. `@every 10s` cron in
    `task/listen.go:8 Start()`, mirroring `ListenSolJob`) that queries `/query` for `pending`
    `provider_orders` with provider `xlayer`, and on `settled` runs the same `OrderProcessing` +
    `sendPaymentNotification` + `MarkProviderOrderPaid` sequence as `HandleOkPayNotify`
    (`okpay_service.go:260-282`). The one piece OKPay has no analogue for. Poll window must be bounded by the
    order expiry (`system.order_expiration_time`) and X Layer's `expireSeconds` (default 1800).
11. `model/request/order_request.go:62` — add `xlayer` to the `Network` example; `route/router.go` needs no
    new route (reuse `/pay/switch-network`, `/pay/checkout-counter-resp`, `/pay/check-status`); no
    `/payments/xlayer/v1/notify` route is needed.

### Cannot be done from this repo alone

- **Admin settings page** (`/settings/okpay` → new `/settings/xlayer`): hardcoded React form in built
  `www/assets/okpay-DzwNhqlx.js` (8 named fields, PUTs `{group:"okpay",key,value,type}` items). Backend only
  stores/returns flat rows; without the frontend source (absent from this repo) a new admin form cannot be
  built here — the only backend-side workaround is calling `PUT /admin/api/v1/settings` directly (curl /admin
  API) with `xlayer.*` keys.
- **Cashier X Layer option**: the "choose OKPay" panel, token radios (`okpay-token-*`), `okpay?.enabled &&
  allow_tokens` gating, and the `window.open(payment_url, 'okpay_checkout', 'popup,…')` redirect are all
  hardcoded in built `www/assets/_trade_id-B2s-YYlo.js`. The backend can expose an `okpay`-equivalent config +
  `payment_url`, and the cashier already treats any order with `payment_url && !receive_address` as a hosted
  panel — but the X Layer **selection entry point** (an "X Layer" method button + its token list) will not
  appear until the frontend is rebuilt from its (missing) source with an `xlayer` branch mirroring the `okpay`
  branch.
- **What the backend can still prove without frontend changes**: create an order via `POST
  /payments/gmpay/v1/order/create-transaction`, then `POST /pay/switch-network {network:"xlayer",
  token:"USDC"}` and verify the response carries `payment_url` = X Layer `checkoutUrl`; polling + paid-marking
  + merchant notify can all be verified server-side.
