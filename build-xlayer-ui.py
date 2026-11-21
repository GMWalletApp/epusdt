#!/usr/bin/env python3
"""Build the X Layer admin settings page by cloning the OkPay settings chunk.

The epusdt repo ships only the BUILT frontend (src/www/assets, no source,
no source maps, no separate frontend repo under GMWalletApp). The OkPay
settings page is a self-contained lazy chunk (okpay-*.js) wired into the
TanStack Router route tree + settings sidebar nav + command-palette search.
This script:

1. Clones okpay-DzwNhqlx.js -> xlayer-7Kx2Qw9Z.js with X Layer fields
   (api_key / api_secret / passphrase / to_address / timeout_seconds /
   allow_tokens / return_url; api_url display-only), labels as plain
   strings (no i18n entries exist for X Layer), same settings-group
   read/write helpers (H/U/V/G from lib-DyUkl8FG.js).
2. Registers the route in router-aZWjwYfd.js:
   - appends the new chunk to the __vite__mapDeps list (index 141)
   - adds `xl=E('/_authenticated/settings/xlayer')...` after the okpay route
   - adds `Xl=xl.update({id:'/xlayer',...})` after Vt
   - adds AuthenticatedSettingsXlayerRoute:Xl to the settings children object
3. Adds the sidebar entry in route-BkjShyH8.js and the command-palette
   entry in search-provider-DPXFLA4q.js.
4. Runs node --check on every touched file.

Re-run is idempotent ONLY on a pristine tree: after the first run the
asserts for "old" substrings fail on the already-patched files. Restore
from git (git checkout -- src/www/assets) before re-running.
"""
import subprocess
import sys
from pathlib import Path

ASSETS = Path(__file__).resolve().parent / "src" / "www" / "assets"
CHUNK = "xlayer-7Kx2Qw9Z.js"


def replace_once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"FAIL {label}: expected 1 occurrence, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def build_chunk():
    src = (ASSETS / "okpay-DzwNhqlx.js").read_text()
    reps = [
        # zod schema: okpay fields -> xlayer fields
        ("J=O({allowTokens:D(),apiUrl:D(),callbackUrl:D().url(m()),enabled:E(),returnUrl:D(),shopId:D().min(1,C()),shopToken:D().min(1,i()),timeoutSeconds:D().min(1,c()).refine(e=>Number.isInteger(Number(e))&&Number(e)>0,{message:v()})});",
         "J=O({allowTokens:D(),apiUrl:D(),apiKey:D().min(1,C()),apiSecret:D().min(1,i()),enabled:E(),passphrase:D().min(1,i()),returnUrl:D(),timeoutSeconds:D().min(1,c()).refine(e=>Number.isInteger(Number(e))&&Number(e)>0,{message:v()}),toAddress:D().min(1,C())});"),
        # form defaults
        ("defaultValues:{allowTokens:`USDT`,apiUrl:``,callbackUrl:``,enabled:!1,returnUrl:``,shopId:``,shopToken:``,timeoutSeconds:`600`}",
         "defaultValues:{allowTokens:`USDC,USDT0,USDG`,apiUrl:``,apiKey:``,apiSecret:``,enabled:!1,passphrase:``,returnUrl:``,timeoutSeconds:`10`,toAddress:``}"),
        # load stored values into the form
        ("allowTokens:X(G(t,`okpay.allow_tokens`,`USDT`)).join(`,`),apiUrl:G(t,`okpay.api_url`),callbackUrl:G(t,`okpay.callback_url`),enabled:[`1`,`true`,`yes`].includes(G(t,`okpay.enabled`).toLowerCase()),returnUrl:G(t,`okpay.return_url`),shopId:G(t,`okpay.shop_id`),shopToken:G(t,`okpay.shop_token`),timeoutSeconds:G(t,`okpay.timeout_seconds`,`600`)",
         "allowTokens:X(G(t,`xlayer.allow_tokens`,`USDC,USDT0,USDG`)).join(`,`),apiUrl:G(t,`xlayer.api_url`),apiKey:G(t,`xlayer.api_key`),apiSecret:G(t,`xlayer.api_secret`),enabled:[`1`,`true`,`yes`].includes(G(t,`xlayer.enabled`).toLowerCase()),passphrase:G(t,`xlayer.passphrase`),returnUrl:G(t,`xlayer.return_url`),timeoutSeconds:G(t,`xlayer.timeout_seconds`,`10`),toAddress:G(t,`xlayer.to_address`)"),
        # settings group query
        ("H(`okpay`)", "H(`xlayer`)"),
        # save (submit) payload
        ("[{group:`okpay`,key:`okpay.enabled`,type:`bool`,value:n.enabled?`true`:`false`},{group:`okpay`,key:`okpay.shop_id`,type:`string`,value:n.shopId},{group:`okpay`,key:`okpay.shop_token`,type:`string`,value:n.shopToken},{group:`okpay`,key:`okpay.timeout_seconds`,type:`int`,value:n.timeoutSeconds},{group:`okpay`,key:`okpay.callback_url`,type:`string`,value:n.callbackUrl}],h())",
         "[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:n.enabled?`true`:`false`},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:n.apiKey},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:n.apiSecret},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:n.passphrase},{group:`xlayer`,key:`xlayer.to_address`,type:`string`,value:n.toAddress},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:n.timeoutSeconds},{group:`xlayer`,key:`xlayer.allow_tokens`,type:`string`,value:n.allowTokens},{group:`xlayer`,key:`xlayer.return_url`,type:`string`,value:n.returnUrl}],h())"),
        # reset payload
        ("[{group:`okpay`,key:`okpay.enabled`,type:`bool`,value:!1},{group:`okpay`,key:`okpay.shop_id`,type:`string`,value:``},{group:`okpay`,key:`okpay.shop_token`,type:`string`,value:``},{group:`okpay`,key:`okpay.api_url`,type:`string`,value:``},{group:`okpay`,key:`okpay.callback_url`,type:`string`,value:``},{group:`okpay`,key:`okpay.return_url`,type:`string`,value:``},{group:`okpay`,key:`okpay.timeout_seconds`,type:`int`,value:600},{group:`okpay`,key:`okpay.allow_tokens`,type:`string`,value:``}],f())",
         "[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:!1},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.to_address`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.api_url`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.return_url`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:10},{group:`xlayer`,key:`xlayer.allow_tokens`,type:`string`,value:``}],f())"),
        # enable switch title/desc
        ('(0,q.jsx)(P,{children:r()}),(0,q.jsx)(F,{children:S()})',
         '(0,q.jsx)(P,{children:"Enable X Layer"}),(0,q.jsx)(F,{children:"Allow checkout orders to be redirected to the OKX hosted page on X Layer."})'),
        # shopId -> API Key
        ("(0,q.jsx)(j,{control:n.control,name:`shopId`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:o()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{autoComplete:`off`,...e})}),(0,q.jsx)(I,{})]})})",
         "(0,q.jsx)(j,{control:n.control,name:`apiKey`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:\"API Key\"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{autoComplete:`off`,...e})}),(0,q.jsx)(I,{})]})})"),
        # shopToken -> API Secret (password input)
        ("(0,q.jsx)(j,{control:n.control,name:`shopToken`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:y()}),(0,q.jsx)(N,{children:(0,q.jsx)(z,{autoComplete:`off`,...e})}),(0,q.jsx)(I,{})]})})",
         "(0,q.jsx)(j,{control:n.control,name:`apiSecret`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:\"API Secret\"}),(0,q.jsx)(N,{children:(0,q.jsx)(z,{autoComplete:`off`,...e})}),(0,q.jsx)(I,{})]})})"),
        # timeout label + placeholder
        ("(0,q.jsx)(P,{children:l()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{inputMode:`numeric`,placeholder:`600`,...e})})",
         "(0,q.jsx)(P,{children:\"Timeout (seconds)\"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{inputMode:`numeric`,placeholder:`10`,...e})})"),
        # apiUrl label + placeholder (stays disabled, display-only like okpay)
        ("(0,q.jsx)(P,{children:u()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{disabled:!0,placeholder:`https://api.okpay.example`,...e})})",
         "(0,q.jsx)(P,{children:\"API URL\"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{disabled:!0,placeholder:`https://web3.okx.com`,...e})})"),
        # callbackUrl -> Passphrase (password input)
        ("(0,q.jsx)(j,{control:n.control,name:`callbackUrl`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:d()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{placeholder:`https://example.com/notify`,...e})}),(0,q.jsx)(I,{})]})})",
         "(0,q.jsx)(j,{control:n.control,name:`passphrase`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:\"Passphrase\"}),(0,q.jsx)(N,{children:(0,q.jsx)(z,{autoComplete:`off`,...e})}),(0,q.jsx)(I,{})]})})"),
        # returnUrl -> Receiving address (editable now, was disabled on okpay)
        ("(0,q.jsx)(j,{control:n.control,name:`returnUrl`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:g()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{disabled:!0,placeholder:`https://example.com/success`,...e})}),(0,q.jsx)(I,{})]})})",
         "(0,q.jsx)(j,{control:n.control,name:`toAddress`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:\"Receiving address (to)\"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{placeholder:`0x...`,...e})}),(0,q.jsx)(I,{})]})})"),
        # allowTokens: editable (was disabled on okpay), new placeholder + desc
        ("(0,q.jsx)(j,{control:n.control,name:`allowTokens`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:p()}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{disabled:!0,placeholder:`USDT,USDC`,...e})}),(0,q.jsx)(F,{children:s()}),(0,q.jsx)(I,{})]})})",
         "(0,q.jsx)(j,{control:n.control,name:`allowTokens`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:\"Allowed tokens\"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{placeholder:`USDC,USDT0,USDG`,...e})}),(0,q.jsx)(F,{children:\"Comma-separated token symbols, e.g. USDC,USDT0,USDG\"}),(0,q.jsx)(I,{})]})})"),
        # page header
        ("(0,q.jsx)(B,{description:n(),title:b(),variant:`section`,children:(0,q.jsx)(Y,{})})",
         "(0,q.jsx)(B,{description:\"Configure OKX Onchain OS Payment (X Layer) hosted checkout.\",title:\"X Layer\",variant:`section`,children:(0,q.jsx)(Y,{})})"),
    ]
    out = src
    for i, (old, new) in enumerate(reps):
        out = replace_once(out, old, new, f"chunk rep {i}")
    if "okpay" in out.lower():
        raise SystemExit("FAIL chunk: 'okpay' still present in new chunk")
    for bad in ("shopId", "shopToken", "callbackUrl"):
        if bad in out:
            raise SystemExit(f"FAIL chunk: {bad!r} still present")
    for good in ("xlayer.api_key", "xlayer.api_secret", "xlayer.passphrase",
                 "xlayer.to_address", "xlayer.allow_tokens", "Enable X Layer"):
        if good not in out:
            raise SystemExit(f"FAIL chunk: {good!r} missing")
    (ASSETS / CHUNK).write_text(out)
    print(f"wrote {CHUNK} ({len(out)} bytes)")


def patch_file(path, reps):
    text = path.read_text()
    for old, new, label in reps:
        text = replace_once(text, old, new, label)
    path.write_text(text)
    print(f"patched {path.name}")


def main():
    build_chunk()
    router = ASSETS / "router-aZWjwYfd.js"
    okpay_route = ("import(`./okpay-DzwNhqlx.js`),__vite__mapDeps([132,1,2,3,4,5,6,7,8,9,109,12,60,14,99,33,28,34,49,86,61,106,18,19,20,21])),`component`)}),")
    xlayer_route = ("xl=E(`/_authenticated/settings/xlayer`)({component:D(()=>k(()=>import(`./xlayer-7Kx2Qw9Z.js`),__vite__mapDeps([141,1,2,3,4,5,6,7,8,9,109,12,60,14,99,33,28,34,49,86,61,106,18,19,20,21])),`component`)}),")
    patch_file(router, [
        ('"assets/password-DKY5aHg5.js"]',
         '"assets/password-DKY5aHg5.js","assets/xlayer-7Kx2Qw9Z.js"]',
         "router mapDeps append"),
        (okpay_route, okpay_route + xlayer_route, "router route def"),
        ("Vt=ut.update({id:`/okpay`,path:`/okpay`,getParentRoute:()=>Y}),",
         "Vt=ut.update({id:`/okpay`,path:`/okpay`,getParentRoute:()=>Y}),Xl=xl.update({id:`/xlayer`,path:`/xlayer`,getParentRoute:()=>Y}),",
         "router route update"),
        ("AuthenticatedSettingsOkpayRoute:Vt,",
         "AuthenticatedSettingsOkpayRoute:Vt,AuthenticatedSettingsXlayerRoute:Xl,",
         "router children object"),
    ])
    patch_file(ASSETS / "route-BkjShyH8.js", [
        ("{title:s(),href:`/settings/okpay`,icon:(0,S.jsx)(u,{size:18})}",
         "{title:s(),href:`/settings/okpay`,icon:(0,S.jsx)(u,{size:18})},{title:\"X Layer\",href:`/settings/xlayer`,icon:(0,S.jsx)(u,{size:18})}",
         "nav entry"),
    ])
    patch_file(ASSETS / "search-provider-DPXFLA4q.js", [
        ("{title:Te(),url:`/settings/okpay`}",
         "{title:Te(),url:`/settings/okpay`},{title:\"X Layer\",url:`/settings/xlayer`}",
         "search entry"),
    ])
    for f in [CHUNK, "router-aZWjwYfd.js", "route-BkjShyH8.js",
              "search-provider-DPXFLA4q.js"]:
        r = subprocess.run(["node", "--check", str(ASSETS / f)],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(r.stdout, r.stderr)
            raise SystemExit(f"FAIL node --check {f}")
        print(f"node --check ok: {f}")
    print("ALL OK")


if __name__ == "__main__":
    main()
