# X Layer (OKX Onchain OS Payment) support — implementation notes

Implemented 2026-11-18 on top of upstream commit `aed4a97`. Base: `src/`
in this directory (cloned from https://github.com/GMWalletApp/epusdt.git).
Full exploration map: `XLAYER-EXPLORATION.md`.

## What it is

A new hosted-checkout provider mirroring the existing OkPay integration:

- Chain: X Layer only, chainId `"196"`.
- Tokens (all 6 decimals): USDC `0xb6ceceab302e2e4948951ee7843fc24e92933061`,
  USD₮0 `0x779ded0c9e1022225f8e0630b35a9b54be713736` (normalized to the ASCII
  symbol `USDT0` everywhere in code and UI), USDG
  `0x4ae46a509f6b1d9056937ba4500cb143933d2dc8`.
- `POST /api/v6/pay/onchainos-payment/create` returns `paymentIntentId` +
  `checkoutUrl`; the buyer is redirected there and signs an EIP-3009
  authorization. Funds settle directly into the merchant `to` address;
  the platform pays gas.
- **No webhook.** Settlement is confirmed by polling
  `POST /query` until `status=settled` — only `settled` counts as paid.
  Redirects and txHash alone never mark an order paid.
- Minimum order: 1 whole token (`tokenAmount >= "1000000"` base units),
  enforced by the provider and pre-checked in `xlayerAmounts`
  (`PayAmountErr`, 10004). A ¥1 order (~0.15 USDC) is rejected before any
  HTTP call.
- Auth: `OK-ACCESS-KEY` / `OK-ACCESS-SIGN` / `OK-ACCESS-PASSPHRASE` /
  `OK-ACCESS-TIMESTAMP`; sign = Base64(HMAC-SHA256(secret,
  timestamp + method + requestPath + raw JSON body)), timestamp ISO 8601 UTC
  with milliseconds. The exact body bytes are signed and sent.

## Backend changes (all Go, no DB migration)

| File | Change |
|---|---|
| `src/model/mdb/settings.go` | `SettingGroupXLayer="xlayer"` + 9 `xlayer.*` keys |
| `src/model/dao/mdb_table_init.go` | Seed defaults: enabled=false, api_url=https://web3.okx.com, timeout=10, allow_tokens="USDC,USDT0,USDG" |
| `src/model/data/settings_data.go` | `GetXLayer*()` getters (mirror `GetOkPay*()`) |
| `src/model/mdb/orders_mdb.go` | `PaymentProviderXLayer = "xlayer"` |
| `src/model/response/support_response.go` | `XLayerPublicConfig` + `XLayer` field on `PublicConfigResponse` (credentials admin-only) |
| `src/controller/comm/supported_asset_controller.go` | X Layer exposed as a pseudo-network entry in `supported_assets` when enabled; `GetPublicConfig` fills the `xlayer` block |
| `src/controller/admin/settings_controller.go` | `xlayer` added to group enums + key docs (API is generic; no logic change) |
| `src/model/request/order_request.go` | `xlayer` added to the switch-network `Network` example |
| `src/model/service/xlayer_service.go` | **New.** Signed create/query client, token contracts, amount math, `PollXLayerProviderOrders()` (settle via `OrderProcessing` + notification on `settled`; expire order on `expired`/`failed`) |
| `src/model/service/order_service.go` | `SwitchNetwork` routes `network=="xlayer"` to `switchToXLayer` / `completeWaitSelectXLayerOrder` (mirror of the OkPay pair); sub-order `ReceiveAddress` stays empty so the cashier renders the hosted panel |
| `src/model/data/order_data.go` | `CompleteWaitSelectXLayerOrderWithTransaction` |
| `src/model/data/provider_order_data.go` | `ListPendingProviderOrdersByProvider` |
| `src/model/service/pay_service.go` | Re-attach stored `PayURL` for `xlayer` orders too |
| `src/task/listen_xlayer_job.go` | **New.** `XLayerPollJob` |
| `src/task/listen.go` | Poll job scheduled `@every 10s` |
| `src/model/service/xlayer_service_test.go` | **New.** 6 tests incl. a published OKX signature vector, amount math, mocked create/query, and a full poll→settle flow |

## How the cashier offers X Layer without a frontend rebuild

The repo ships only the built SPA (`src/www/`, no frontend source), and the
admin settings pages / cashier provider branches are hardcoded per provider
in minified JS. Two facts make a backend-only integration work anyway:

1. The cashier's chain-method option list is built from `supported_assets`
   (`GET /payments/gmpay/v1/config`). When `xlayer.enabled` is true the
   backend appends `{network:"xlayer", displayName:"X Layer",
   tokens:[allow_tokens]}` there, so X Layer appears as a selectable
   network with USDC/USDT0/USDG radios. Selecting it calls the existing
   `POST /pay/switch-network` with `network:"xlayer"`.
2. The cashier's hosted panel renders when `payment_url` is set and
   `receive_address` is empty (`payment_url && !receive_address`). X Layer
   orders keep `ReceiveAddress=""`, so after the switch the cashier shows
   the hosted panel with a button that opens `checkoutUrl` in a popup.
   X Layer is deliberately **not** added as a chain row: there is no
   on-chain listener for it, and a chain row would let GMPay create
   on-chain orders that could never confirm.

## What cannot be done from this repo alone

- **A dedicated admin settings page** (`/settings/xlayer` mirroring
  `/settings/okpay`). The admin UI is a hardcoded form per provider in the
  built JS. Configure via the admin settings API instead — see
  `../epusdt-deploy/configure-xlayer.sh` (prompts for the admin password
  and the OKX credentials; nothing is stored in the script).
- The cashier button/labels come from the generic `supported_assets`
  rendering, so no new cashier chrome was needed.

## Configuration keys (settings table, group `xlayer`)

`enabled` (bool), `api_key`, `api_secret`, `passphrase`,
`api_url` (default https://web3.okx.com), `to_address` (merchant X Layer
receiving address — funds settle here), `return_url` (optional),
`timeout_seconds` (default 10), `allow_tokens` (default "USDC,USDT0,USDG").

## Test evidence (2026-11-18)

- `go build ./...` clean; `go vet` clean; `go test ./...` passes
  (one pre-existing flaky Aptos test fails when run alone with `-count=5`
  on pristine code too — unrelated).
- Sandbox `epusdt` (InstaBox) runs the built binary
  (`version 0.0.0-xlayer-dev`; original v2.0.0 binary backed up in the
  sandbox as `~/epusdt-orig-v2.0.0`):
  - Disabled: `xlayer.enabled=false`, no xlayer in `supported_assets`.
  - Enabled: `supported_assets` gains `xlayer: [USDC, USDT0, USDG]`.
  - ¥10 order → switch-network → `10042` (provider create failed); server
    log shows OKX answered `401 {"msg":"Invalid OK-ACCESS-KEY",
    "code":"50111"}` to the dummy key — the signed request reaches OKX.
  - ¥1 order → switch-network → `10004` (below the 1-token minimum),
    no HTTP call.
  - `XLayerPollJob scheduled successfully (@every 10s)` in startup logs.
  - Cashier UI check: browser task 2026-11-18 11:25 confirmed live — the
    cashier's network dropdown lists Ethereum, BNB Smart Chain, Polygon and
    **X Layer**; selecting X Layer offers USDC (default), USDT0 and USDG
    with no errors. No confirm button was clicked.
- After verification the sandbox was left with X Layer **disabled** and the
  dummy credentials deleted; the log level was restored to `error`.

## 2026-11-18 pm: admin settings UI, real credentials, 50125 blocker, gas-free badge

- Admin UI page (user asked for it in the UI, not the script): the repo
  ships only built frontend assets, so `build-xlayer-ui.py` clones the
  OkPay settings chunk into `xlayer-7Kx2Qw9Z.js` (9 fields: enabled,
  api_key, api_secret, passphrase, to_address, timeout_seconds,
  allow_tokens editable; api_url display-only), registers route
  `/_authenticated/settings/xlayer` in `router-aZWjwYfd.js` (chunk
  appended to `__vite__mapDeps` at index 141), and adds the sidebar entry
  (`route-BkjShyH8.js`) + command-palette entry
  (`search-provider-DPXFLA4q.js`). Rebuild the Go binary afterwards:
  `www` is `go:embed`ded. `build-xlayer-badge.py` also fixed the
  copy-paste OkPay wording on the save button/toast ("保存 X Layer 配置").
- Real credentials configured via the UI 2026-11-18 11:38 (browser task,
  user logged in as admin; values never written to files/memory):
  enabled=true, to_address=0xb8cadda9bb5539884509b96d8303ec8c5f0d7728,
  timeout 10, allow_tokens USDC,USDT0,USDG. Browser-verified: page loads,
  saves, values persist after reload; OkPay page unaffected.
- Gas-free badge (`build-xlayer-badge.py`): the network-label component
  in `labels-BwEfuu7e.js` (`r`) and the payment-step network pill in
  `_trade_id-B2s-YYlo.js` (`hi`) append a small green "免 Gas" pill when
  network is xlayer. Only Tailwind classes already present in the built
  `index-*.css` are used (e.g. `bg-emerald-500/10`, `text-emerald-700`,
  `dark:text-emerald-400`, `px-1.5`, `py-0.5`, `text-xs`) — Tailwind only
  ships classes seen at build time, so a new utility would render unstyled.
- Blocker found 2026-11-18 11:45–11:47: clicking confirm on a ¥9.9 order
  (B8f6x24FtuJtf121cF9kC6ly, Dujiao DJ20261118114443753873) returns `10042`;
  server log shows OKX `401 {"msg":"Your API key or regions have no
  access to current services","code":"50125"}`. The key is valid (a wrong
  key gives 50111); the key/account has no access to the Onchain OS
  Payment merchant service. USDC and USDT0 fail identically — not
  token-specific. Note: picking network/token in the cashier only changes
  local UI state; the OKX create call happens on confirm. Log level was
  temporarily raised to `info` for diagnosis and restored to `error`.
- The ¥9.9 test order expired ~11:54. A fresh ¥7+ order is needed for the
  next live test, after the key's service access is enabled on OKX's side.

## Still needed for a live test

The OKX API key needs access to the Onchain OS Payment service (currently
401 / code 50125). Once enabled on OKX's side, place an order of ~¥7+
(≥1 token), pick X Layer + a token in the cashier, pay on the OKX hosted
page, and watch the poll job mark the order paid. No real payment has been
made through this path yet.

## 2026-11-20: intermediate page removed entirely (v2)

v1 (2026-11-18, `build-xlayer-redirect.py`) redirected to OKX after the
switch-network success handler and from the payment step's `useEffect`,
but the intermediate page ("正在打开 OkPay 支付窗口") still flashed:
the v1 patch ran *after* the order-state update, so React re-rendered
to the payment step before `window.location.href` took effect, and the
`useEffect` only fires after the page mounts and paints. Refresh /
direct-open of an already-switched order never ran the first patch.

v2, same script, still only `_trade_id-B2s-YYlo.js`, OKPay flow untouched:
1. `Oe` (switch success): the xlayer redirect now runs *before* any
   `setState` — with a payment_url, navigate immediately and return
   without calling `f()`/`v()`, so no re-render to the payment step.
2. `Zt` (panel dispatcher): in the `payment` case, if the order is
   xlayer with a payment_url, navigate during render and return only a
   minimal "正在跳转 OKX…" placeholder instead of `Vt` — the intermediate
   page never renders. This covers refresh / direct-open.
3. `Vt`'s `useEffect` redirect is kept as a last-resort fallback only.

Rebuild order (pristine tree first): `git checkout -- src/www/assets` →
`build-xlayer-ui.py` → `build-xlayer-badge.py` →
`build-xlayer-redirect.py` → `rename-xlayer-chunks.py` → `make
linux-amd64` (CGO_ENABLED=0, BUILD_TAG=0.0.0-xlayer-dev) → `instabox cp`
binary to the sandbox → `docker cp` into `epusdt-epusdt-1:/app/epusdt`
→ `docker restart`. Verified 2026-11-20 17:14–17:15 in the browser:
fresh ¥1 order (confirm path) and direct-open of an already-switched
order both land straight on the OKX checkout with no intermediate page;
no payment, no wallet connected. See NOTES.md for hashes and order IDs.

## 2026-11-20: USDT0 displayed as USDT + correct USDT logo (display only)
- Cashier showed `USDT0` with a grey placeholder circle (no logo) while USDC had its real logo. User asked: show `USDT`, use the correct Tether logo like the others.
- Display-only: `USDT0` stays in config `allow_tokens`, the switch-network token value, the OKX create-order payload, and the DB. Only buyer-facing text/icons change.
- Script `build-xlayer-usdt-display.py` (runs after `build-xlayer-redirect.py`, before `rename-xlayer-chunks.py`): (1) `crypto-icon-yn0aBrU6.js` `u` map gains a `USDT0` entry pointing at the USDT ethereum contract `0xdAC17F958D2ee523a2206206994597C13D831ec7`, so the Trust Wallet icon mechanism renders the real Tether logo; (2) `labels-BwEfuu7e.js` function `i` normalizes label + icon symbol `USDT0` -> `USDT`; (3) `_trade_id-B2s-YYlo.js` `Ei` maps the payment-step amount token `USDT0` -> `USDT` for display. Rebuild order is now: git checkout -- src/www/assets → build-xlayer-ui.py → build-xlayer-badge.py → build-xlayer-redirect.py → build-xlayer-usdt-display.py → rename-xlayer-chunks.py → go build. Binary sha256 `2f346cbd...` deployed 2026-11-20 ~18:09. Browser verification: ¥10 test order `Iz2JHGaisN99FyEGiexslGF-`, pending.

## 2026-11-20: USDG gets its real logo (display only)
- Cashier token dropdown showed USDG as text only, no icon. Cause: the icon map `u` in `crypto-icon-yn0aBrU6.js` had no `USDG` entry at all (0 hits), so the icon component fell back to a grey circle that is nearly invisible on the dark theme. The earlier USDT0 fix only added USDT0.
- Logo source verified to exist: `GMWalletApp/assets` (branch `main`), `blockchains/ethereum/assets/0xe343167631d89B6Ffc58B88d6b7fB0228795491D/logo.png` — the Paxos USDG Ethereum contract address (the X Layer contract `0x4ae46a509f6b1d9056937ba4500cb143933d2dc8` is different; the icon map uses the Ethereum address, same as USDT/USDC).
- Implemented by zcode (Flash, default) this time — zcode's new-process model failure was fixed and verified earlier the same day (credential-secret default location). Task prompt: `ZCODE-USDG-LOGO-PROMPT.md`. It extended `build-xlayer-usdt-display.py` to add `USDG:{network:'ethereum',contractAddress:'0xe343167631d89B6Ffc58B88d6b7fB0228795491D'}` right after the USDT0 entry; no backend changes. Rebuild order unchanged; all 7 touched JS files pass `node --check`. Binary sha256 `d7e8f421780fbb5da6271865183273261d436970393b888a2bcefe59cd97f87c` deployed 2026-11-20 ~18:45 (one hiccup: first `docker cp ~/epusdt-new` failed because `~` expanded locally; fixed with the sandbox absolute path `/data/home/epusdt-new`). curl verification: live crypto-icon contains the full USDG entry, config `allow_tokens` still `USDC,USDT0,USDG`, USDG logo.png 200 on all three CDN mirrors (256x256 PNG, 9612 bytes). Browser verification 2026-11-20 18:46-18:48: fresh ¥10 test order `mBzdyvEo6jbfe6Sys3OO_h5g` (actual 1.47 USDT) — USDC blue circle white $, USDT teal circle white T, USDG black circle white bold G, identical in dropdown and closed selector; green "免Gas" badge still present; no payment, no wallet, no confirm. Note: zcode's report described the USDG logo as "blue-purple G"; the actual render is black-background white G — the latter is correct (the canonical Trust Wallet USDG logo). Resolved in the same round: the browser first reported X Layer amounts of 1.40 (USDC/USDT) and 1.41 (USDG) while the order's actual_amount was 1.47 — that turned out to be a browser misread. Re-checked on fresh order `_cSWfAjjXUh3XxDhriT3q8Ox` (actual 1.48): the switch popup's X Layer/USDT row reads exactly "1.48 USDT", matching the backend actual_amount, and the main amount line reads "10 CNY". The frontend has no rate/quote endpoint besides checkout-counter-resp, so the displayed amount always equals the order's actual_amount.

## 2026-11-20: integrated into the admin config flow (design doc: XLAYER-ADMIN-INTEGRATION-DESIGN.md)

X Layer is no longer a pseudo-network bolted onto supported_assets; it now lives in the three existing admin flows:

- **Address management** (`/addresses`): the network dropdown is driven by `/admin/api/v1/chains`, so the seeded `chains` row (network `xlayer`, display name "X Layer") makes X Layer selectable. `xlayer.to_address` is **migrated at startup** (`seedXLayerWalletFromSettings`) into `wallet_address` (status=enabled, source=manual) and `data.GetXLayerToAddress()` now reads that table first (oldest enabled row), settings key only a fallback. Disabling the address removes the cashier entry (existing generic rule). The address is stored as typed (xlayer deliberately NOT in `isEVMNetwork`).
- **Chains & tokens** (`/chains`): seeded `chains` + 3 `chain_tokens` rows (USDC/USDT0/USDG, contracts matching `xlayerTokenContracts`). `data.GetXLayerAllowTokens()` now reads enabled `chain_tokens` (settings key fallback only while no rows exist) — the per-token switches genuinely drive the cashier. `buildSupportedAssets()` lost its xlayer pseudo block; the generic path emits the entry, gated by the settings master switch (`xlayer.enabled`) and filtered by the Go contract map (`service.IsPayableXLayerToken`).
- **Settings**: the old "X Layer" page (`/settings/xlayer`, chunk `xlayer-7Kx2Qw9Z.js`) is retitled **"OKX OnchainOS 配置"** (sidebar + command palette + page header), keeps enabled/API Key/API Secret/Passphrase/API URL(readonly)/timeout, **gains a return_url input**, **loses to_address + allow_tokens inputs** (save/reset payloads stop writing both keys), and carries an always-visible Chinese 开通引导 card (dev-portal steps + error meanings: 50125 = key lacks merchant payment permission, 50111 = wrong key, 61009 = duplicate merchantPaymentId).
- Guard: `CreateTransaction` rejects `network=="xlayer"` with `ChainNotEnabled` exactly as before the chains row existed (prevents dead on-chain orders; placeholder + switch-network remains the only entry).
- New patch script `build-xlayer-admin-integration.py` (runs after `build-xlayer-usdt-display.py`, before `rename-xlayer-chunks.py`). **Full rebuild order is now: git checkout -- src/www/assets → build-xlayer-ui.py → build-xlayer-badge.py → build-xlayer-redirect.py → build-xlayer-usdt-display.py → build-xlayer-admin-integration.py → rename-xlayer-chunks.py → go build.** (New script documents this; it only patches `xlayer-7Kx2Qw9Z.js`, `route-BkjShyH8.js`, `search-provider-DPXFLA4q.js`.)
- Deployed 2026-11-20 ~20:24, binary sha256 `a8678775b7005f0de0630704c928f5e02f9310e1041ca3a350e980289714a712` (statically linked). Startup log shows `migrated xlayer.to_address into wallet_address (id=4)`. curl checks: `/`, `/sign-in`, `/payments/gmpay/v1/config` 200; config `xlayer.allow_tokens` still `["USDC","USDT0","USDG"]`; supported_assets has exactly one xlayer entry with `[USDC, USDG, USDT0]` (alphabetical — same sorting as every other chain); all 11 touched chunks 200. ¥10 test order `zubdgCrmQ7H2FvShk-uwKXzf` created via `~/xlayer-create-only.py` (create-only, no switch, no payment), cashier page + `/pay/checkout-counter-resp` 200. Note: cashier token dropdown order is now alphabetical (USDT listed after USDG); default first token still USDC.
- Test suite: all packages pass except the **pre-existing** `TestProcessAptosLedgerRoundMarksMatchingUSDTOrderPaid`, verified failing on pristine HEAD (aed4a97) via a throwaway git worktree — unrelated to this change (matches the flaky-Aptos note above).
- **2026-11-20 2nd review round (user feedback on the settings page):** (1) the amber error-code block (50125/50111) inside the 开通引导 card is removed entirely — the guide is now exactly 5 numbered steps + a prominent dev-portal button, and the chunk bans `50125/50111/61009/常见报错/amber` at build time. (2) Passphrase is now visually fully separated: both credential groups are symmetric top-level blocks of the form — a `text-base font-semibold` section heading + description OUTSIDE a `rounded-md border p-4` box holding only its own fields (Key+Secret together in one box; timeout/API-URL knobs stay plain), then a full-width `border-t` divider, then the standalone "Passphrase（密码）" block with the same structure. Field names, zod schema, load/save/reset payloads and backend untouched. Root causes addressed: the old layout had an unboxed Key/Secret group with only small labels next to a single bordered Passphrase card (read as one group), and the chunk had kept its Vite-hashed name since first shipping (stale browser copies up to 24h) — `rename-xlayer-chunks.py` now renames it `xlayer-7Kx2Qw9Z.js` → `xlayer-XL2.js` (router re-references it; route path still `/settings/xlayer`). Patched chunks in `build-xlayer-admin-integration.py` + `rename-xlayer-chunks.py` had been edited in the prior session but the tree was never rebuilt; this round re-ran the full standard order and deployed. Binary sha256 `1b4ee8a2a4647f628ab39523d09f67a7e6014a74dbeb31eab1a71ace6ac96d45`, deployed 2026-11-20 ~21:47 (instabox cp sha256-verified in sandbox; container restarted clean, version banner 0.0.0-xlayer-dev). curl (host-local + public `https://8000-agqzyqhqzzuvjltnmbmwhrarti.instabox.tech`): `/`, `/sign-in`, config, `xlayer-XL2/router-XL2/index-XL2/_trade_id-*-XL2/labels-XL2/chains-XL2` all 200; live chunk has 0 hits for 50125/50111/61009/amber, exactly one `border-t`, two `rounded-md border p-4` boxes, two `text-base font-semibold` headings; config `xlayer.enabled` true, `allow_tokens` `["USDC","USDT0","USDG"]`. go vet clean; tests pass in `controller/admin, controller/comm, model/dao, model/data, model/service, task` (the Aptos failure named above no longer exists as of 2026-11-20). `~/xlayer-create-only.py` had been lost to a sandbox home reset and was recreated as a local Python script (fetches pid/secret in-memory from the sandbox SQLite via instabox exec, signs HMAC-SHA256 locally, POSTs to the public URL; secrets never printed); validated with ¥10 create-only orders `r3UCkRbGemn4w7xInotHBB8X` and `RrmVaj3MRc4kPfvG4FUczS_B` (status=4 placeholders, no switch, no payment; cashier page + checkout-counter-resp 200). DB settings/chains/wallet rows untouched this round.
