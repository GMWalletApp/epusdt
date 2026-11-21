#!/usr/bin/env python3
"""Integrate the X Layer settings page into the admin config flow.

Requirement 3 + 4 of XLAYER-ADMIN-INTEGRATION-DESIGN.md, plus the
2026-11-20 review fixes. Runs AFTER build-xlayer-ui.py, build-xlayer-
badge.py, build-xlayer-redirect.py and build-xlayer-usdt-display.py
(it patches their output) and BEFORE rename-xlayer-chunks.py. Only
three files are touched:

1. xlayer-7Kx2Qw9Z.js -- the page previously titled "X Layer" becomes
   "OKX OnchainOS 配置", a first-class settings page parallel to OkPay:
   - page header / enable switch / save button / save toast in Chinese
   - to_address + allow_tokens inputs REMOVED (address now lives in
     地址管理 via wallet_address, token switches in 链与代币 via
     chain_tokens; the backend no longer treats the settings keys as
     authoritative). The save/reset payloads stop writing both keys so
     a save here can never clobber the new sources of truth.
   - the return_url input REMOVED again (2026-11-20 review): the admin
     must not type the post-payment return URL; the backend derives it
     when creating the OKX order (merchant redirect_url, else the
     cashier page). The zod field, defaults, load, save and reset
     payloads drop it so no dead field survives.
   - the three credential fields share ONE bordered card (2026-11-20
     3rd review: the 2nd-review split into two sections with a border-t
     divider was the WRONG reading -- the user wants all three IN one
     box). Card titled 「API 凭证」 (text-base font-semibold heading +
     desc outside the box); inside: API Key + API Secret side by side
     (grid md:grid-cols-2), Passphrase full-width below. No internal
     divider. (2026-11-21 4th review: the explanatory sentence under
     the Passphrase field is deleted entirely.) Field names, the zod
     schema, the load/save/reset payloads and the save logic are
     untouched by the card change -- layout only.
   - API 地址 is STATIC TEXT, not an input (2026-11-21 4th review): the
     base URL is fixed in the backend constant (xlayerAPIBaseURL in
     model/service/xlayer_service.go), so the apiUrl zod field, form
     default, load, save and reset payloads drop it -- the page no
     longer reads or writes the xlayer.api_url settings key. The page
     shows 「API 地址：https://web3.okx.com（固定）」 next to the
     timeout field instead of the old disabled input. The leftover
     settings row itself is NOT deleted.
   - the 开通引导 card: numbered steps with breathing room and the
     dev-portal link as a prominent primary button. The 50125/50111
     error-code block REMOVED (2026-11-20 2nd review: no error-code
     text anywhere on the page; steps + dev-portal button only).
     Content trimmed: the address/token-management steps are gone
     (each has its own page), as are the 61009/timeout notes. Only
     Tailwind classes already present in the built index-*.css are
     used (verified: space-y-1/2/3/4/6 rounded-md border
     bg-muted/30 p-3 p-4 text-sm text-xs text-base font-medium
     font-semibold leading-relaxed text-muted-foreground flex
     items-start justify-center gap-2 h-5 w-5 min-w-0 shrink-0
     rounded-full bg-emerald-500/10 text-emerald-700
     dark:text-emerald-400 inline-flex px-3 py-1 text-primary-foreground
     transition-colors hover:bg-primary/90 flex-wrap break-all
     underline-offset-4).
2. route-BkjShyH8.js -- sidebar title "X Layer" -> "OKX OnchainOS 配置".
3. search-provider-DPXFLA4q.js -- command palette title likewise.

The route stays /settings/xlayer so existing deep links keep working.

Rebuild sequence (standing convention, see design doc):
  git checkout -- src/www/assets
  python3 build-xlayer-ui.py
  python3 build-xlayer-badge.py
  python3 build-xlayer-redirect.py
  python3 build-xlayer-usdt-display.py
  python3 build-xlayer-admin-integration.py   <- this script
  python3 rename-xlayer-chunks.py
  go build (from src/: BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64)

Re-run is only valid on a pristine tree (see build-xlayer-ui.py).
"""
import subprocess
from pathlib import Path

ASSETS = Path(__file__).resolve().parent / "src" / "www" / "assets"

# --- building blocks for the guide card (requirement 1 of the 2026-11-20
# 2nd review: only dev-portal -> merchant access -> credentials -> save ->
# small test order; the 50125/50111 error-code block is gone entirely) ---

STEP_NUM = ("(0,q.jsx)(`span`,{className:`flex h-5 w-5 shrink-0 items-center "
            "justify-center rounded-full bg-emerald-500/10 text-xs font-semibold "
            "text-emerald-700 dark:text-emerald-400`,children:%s})")

DEVPORTAL_BTN = ("(0,q.jsx)(`a`,{className:`inline-flex items-center gap-2 "
                 "rounded-md bg-primary px-3 py-1 text-xs font-medium "
                 "text-primary-foreground transition-colors hover:bg-primary/90`,"
                 "href:`https://web3.okx.com/onchainos/dev-portal`,rel:`noreferrer`,"
                 "target:`_blank`,children:\"打开 dev-portal："
                 "web3.okx.com/onchainos/dev-portal\"})")


def _step(num, body):
    return ("(0,q.jsxs)(`div`,{className:`flex items-start gap-2`,children:["
            + STEP_NUM % ('`%s`' % num) + ","
            + body + "]})")


def _step_text(text):
    return ("(0,q.jsx)(`p`,{className:`leading-relaxed text-muted-foreground`,"
            "children:\"%s\"})" % text)


def _guide():
    steps = [
        _step(1, "(0,q.jsxs)(`div`,{className:`min-w-0 space-y-2`,children:["
              + _step_text("打开 OKX OnchainOS 开发者门户，登录 OKX 账号，创建应用（或选择已有应用）。")
              + "," + DEVPORTAL_BTN + "]})"),
        _step(2, _step_text("为该应用申请开通 Onchain OS Payment 商家支付权限。")),
        _step(3, _step_text("在应用凭证页生成 API Key 与 Secret，并设置 Passphrase，三样只展示一次，请妥善保存。")),
        _step(4, _step_text("把三样凭证回填到本页对应输入框，打开上方开关并保存。")),
        _step(5, _step_text("用一笔小额测试单验证：买家支付完成后，系统轮询到 settled 才算收款成功。")),
    ]
    return ("(0,q.jsxs)(`div`,{className:`space-y-3 rounded-md border bg-muted/30 "
            "p-4 text-sm`,children:["
            "(0,q.jsx)(`p`,{className:`font-medium`,children:\"开通引导 · OKX Onchain OS Payment（X Layer）\"}),"
            "(0,q.jsxs)(`div`,{className:`space-y-3`,children:[" + ",".join(steps) + "]})]})")


GUIDE = _guide()

# --- form fields exactly as build-xlayer-ui.py emits them ---

FIELD_API_KEY = ('(0,q.jsx)(j,{control:n.control,name:`apiKey`,render:({field:e})=>'
                 '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"API Key"}),'
                 '(0,q.jsx)(N,{children:(0,q.jsx)(R,{autoComplete:`off`,...e})}),'
                 '(0,q.jsx)(I,{})]})})')

FIELD_API_SECRET = ('(0,q.jsx)(j,{control:n.control,name:`apiSecret`,render:({field:e})=>'
                    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"API Secret"}),'
                    '(0,q.jsx)(N,{children:(0,q.jsx)(z,{autoComplete:`off`,...e})}),'
                    '(0,q.jsx)(I,{})]})})')

FIELD_TIMEOUT_CN = ('(0,q.jsx)(j,{control:n.control,name:`timeoutSeconds`,render:({field:e})=>'
                    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"接口超时（秒）"}),'
                    '(0,q.jsx)(N,{children:(0,q.jsx)(R,{inputMode:`numeric`,placeholder:`10`,...e})}),'
                    '(0,q.jsx)(I,{})]})})')

# 2026-11-21 4th review: API 地址 is no longer an input at all. The base
# URL is a fixed backend constant (xlayerAPIBaseURL in
# model/service/xlayer_service.go); the page only tells the operator
# where requests go. Classes verified in the built index-*.css:
# text-sm font-medium break-all text-muted-foreground leading-relaxed.
STATIC_API_URL = ('(0,q.jsxs)(`div`,{className:`space-y-2`,children:['
                  '(0,q.jsx)(`p`,{className:`text-sm font-medium`,children:"API 地址"}),'
                  '(0,q.jsx)(`p`,{className:`break-all text-muted-foreground text-sm leading-relaxed`,'
                  'children:"https://web3.okx.com（固定）"})]})')

FIELD_PASSPHRASE = ('(0,q.jsx)(j,{control:n.control,name:`passphrase`,render:({field:e})=>'
                    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"Passphrase"}),'
                    '(0,q.jsx)(N,{children:(0,q.jsx)(z,{autoComplete:`off`,...e})}),'
                    '(0,q.jsx)(I,{})]})})')

# The old middle of the form, exactly as build-xlayer-ui.py leaves it:
# grid(apiKey, apiSecret, timeout EN label) + apiUrl(EN label) +
# grid(passphrase, toAddress). Replaced by (2026-11-20 3rd review:
# the 2nd-review split into two sections + border-t divider was the
# wrong reading -- the user wants all three credential fields IN one
# box):
#   one "API 凭证" card -- heading + desc OUTSIDE a bordered box that
#     contains ALL THREE fields: Key + Secret side by side
#     (grid md:grid-cols-2), Passphrase full-width below. No divider
#     inside the card. (2026-11-21 4th review: the explanatory sentence
#     that used to sit under the Passphrase field is deleted entirely.)
#   knobs grid (timeout field, static API 地址 text; 2026-11-21 4th
#     review replaced the old disabled api-url input with plain text).
#     Classes verified in the built index-*.css: text-base font-semibold
#     space-y-4 space-y-1 space-y-2 space-y-6 text-sm text-muted-
#     foreground leading-relaxed rounded-md border p-4 grid gap-6
#     md:grid-cols-2 text-sm font-medium break-all.
#   The replacement ends WITHOUT a trailing comma: the pristine chunk
#     supplies the comma before the next form child (the 2nd-review
#     version ended with one and left a `,,` elision in the children
#     array -- React tolerates it, but it is still a wart; gone now).
FORM_MIDDLE_OLD = (
    '(0,q.jsxs)(`div`,{className:`grid gap-6 md:grid-cols-2`,children:['
    + FIELD_API_KEY + ',' + FIELD_API_SECRET + ','
    '(0,q.jsx)(j,{control:n.control,name:`timeoutSeconds`,render:({field:e})=>'
    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"Timeout (seconds)"}),'
    '(0,q.jsx)(N,{children:(0,q.jsx)(R,{inputMode:`numeric`,placeholder:`10`,...e})}),'
    '(0,q.jsx)(I,{})]})})]}),'
    '(0,q.jsx)(j,{control:n.control,name:`apiUrl`,render:({field:e})=>'
    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"API URL"}),'
    '(0,q.jsx)(N,{children:(0,q.jsx)(R,{disabled:!0,placeholder:`https://web3.okx.com`,...e})}),'
    '(0,q.jsx)(I,{})]})}),'
    '(0,q.jsxs)(`div`,{className:`grid gap-6 md:grid-cols-2`,children:['
    + FIELD_PASSPHRASE + ','
    '(0,q.jsx)(j,{control:n.control,name:`toAddress`,render:({field:e})=>'
    '(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"Receiving address (to)"}),'
    '(0,q.jsx)(N,{children:(0,q.jsx)(R,{placeholder:`0x...`,...e})}),'
    '(0,q.jsx)(I,{})]})})]})'
)

FORM_MIDDLE_NEW = (
    # one credentials card: heading + desc outside, ALL THREE fields inside
    '(0,q.jsxs)(`div`,{className:`space-y-4`,children:['
    '(0,q.jsxs)(`div`,{className:`space-y-1`,children:['
    '(0,q.jsx)(`p`,{className:`text-base font-semibold`,children:"API 凭证"}),'
    '(0,q.jsx)(`p`,{className:`text-muted-foreground text-sm leading-relaxed`,'
    'children:"在 OKX OnchainOS 开发者门户（dev-portal）的应用凭证页生成 Key 与 Secret，并自行设置 Passphrase。"})]}),'
    '(0,q.jsx)(`div`,{className:`rounded-md border p-4`,children:'
    '(0,q.jsxs)(`div`,{className:`space-y-6`,children:['
    # row 1: Key + Secret side by side
    '(0,q.jsxs)(`div`,{className:`grid gap-6 md:grid-cols-2`,children:['
    + FIELD_API_KEY + ',' + FIELD_API_SECRET + ']}),'
    # row 2: Passphrase full-width; the note under it is gone
    # (2026-11-21 4th review)
    + FIELD_PASSPHRASE + ']})})]}),'
    # knobs: timeout field + STATIC API 地址 text (no input; the base URL
    # is fixed in the backend constant); ends WITHOUT a trailing comma --
    # the pristine chunk provides it
    '(0,q.jsxs)(`div`,{className:`grid gap-6 md:grid-cols-2`,children:['
    + FIELD_TIMEOUT_CN + ',' + STATIC_API_URL + ']})'
)


def replace_once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"FAIL {label}: expected 1 occurrence, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def patch_chunk():
    p = ASSETS / "xlayer-7Kx2Qw9Z.js"
    t = p.read_text()
    reps = [
        # 1. zod schema: drop allowTokens + toAddress + returnUrl, and
        #    apiUrl too (2026-11-21 4th review: fixed backend base URL)
        ("J=O({allowTokens:D(),apiUrl:D(),apiKey:D().min(1,C()),apiSecret:D().min(1,i()),enabled:E(),passphrase:D().min(1,i()),returnUrl:D(),timeoutSeconds:D().min(1,c()).refine(e=>Number.isInteger(Number(e))&&Number(e)>0,{message:v()}),toAddress:D().min(1,C())});",
         "J=O({apiKey:D().min(1,C()),apiSecret:D().min(1,i()),enabled:E(),passphrase:D().min(1,i()),timeoutSeconds:D().min(1,c()).refine(e=>Number.isInteger(Number(e))&&Number(e)>0,{message:v()})});",
         "zod schema"),
        # 2. form defaults: drop allowTokens + toAddress + returnUrl + apiUrl
        ("defaultValues:{allowTokens:`USDC,USDT0,USDG`,apiUrl:``,apiKey:``,apiSecret:``,enabled:!1,passphrase:``,returnUrl:``,timeoutSeconds:`10`,toAddress:``}",
         "defaultValues:{apiKey:``,apiSecret:``,enabled:!1,passphrase:``,timeoutSeconds:`10`}",
         "form defaults"),
        # 3. load stored values: stop reading to_address/allow_tokens/
        #    return_url/api_url (2026-11-21 4th review: api_url no longer
        #    read -- the base URL is a fixed backend constant)
        ("n.reset({allowTokens:X(G(t,`xlayer.allow_tokens`,`USDC,USDT0,USDG`)).join(`,`),apiUrl:G(t,`xlayer.api_url`),apiKey:G(t,`xlayer.api_key`),apiSecret:G(t,`xlayer.api_secret`),enabled:[`1`,`true`,`yes`].includes(G(t,`xlayer.enabled`).toLowerCase()),passphrase:G(t,`xlayer.passphrase`),returnUrl:G(t,`xlayer.return_url`),timeoutSeconds:G(t,`xlayer.timeout_seconds`,`10`),toAddress:G(t,`xlayer.to_address`)})",
         "n.reset({apiKey:G(t,`xlayer.api_key`),apiSecret:G(t,`xlayer.api_secret`),enabled:[`1`,`true`,`yes`].includes(G(t,`xlayer.enabled`).toLowerCase()),passphrase:G(t,`xlayer.passphrase`),timeoutSeconds:G(t,`xlayer.timeout_seconds`,`10`)})",
         "load values"),
        # 4. save payload: only enabled/api_key/api_secret/passphrase/timeout
        #    (order kept deterministic)
        ("[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:n.enabled?`true`:`false`},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:n.apiKey},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:n.apiSecret},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:n.passphrase},{group:`xlayer`,key:`xlayer.to_address`,type:`string`,value:n.toAddress},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:n.timeoutSeconds},{group:`xlayer`,key:`xlayer.allow_tokens`,type:`string`,value:n.allowTokens},{group:`xlayer`,key:`xlayer.return_url`,type:`string`,value:n.returnUrl}],",
         "[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:n.enabled?`true`:`false`},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:n.apiKey},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:n.apiSecret},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:n.passphrase},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:n.timeoutSeconds}],",
         "save payload"),
        # 5. reset payload: no longer wipes to_address/allow_tokens/
        #    return_url/api_url (2026-11-21 4th review: the page never
        #    writes the legacy api_url key again)
        ("[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:!1},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.to_address`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.api_url`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.return_url`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:10},{group:`xlayer`,key:`xlayer.allow_tokens`,type:`string`,value:``}],f())",
         "[{group:`xlayer`,key:`xlayer.enabled`,type:`bool`,value:!1},{group:`xlayer`,key:`xlayer.api_key`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.api_secret`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.passphrase`,type:`string`,value:``},{group:`xlayer`,key:`xlayer.timeout_seconds`,type:`int`,value:10}],f())",
         "reset payload"),
        # 6. enable switch copy
        ('(0,q.jsx)(P,{children:"Enable X Layer"}),(0,q.jsx)(F,{children:"Allow checkout orders to be redirected to the OKX hosted page on X Layer."})',
         '(0,q.jsx)(P,{children:"启用 X Layer（OKX OnchainOS）支付"}),(0,q.jsx)(F,{children:"开启后，收银台可以把订单切换到 OKX 托管收银台（X Layer 链，免 Gas）。"})',
         "enable switch copy"),
        # 7. form middle: ONE "API 凭证" card holding Key/Secret/Passphrase
        #    (also drops the toAddress input; CN labels baked in)
        (FORM_MIDDLE_OLD, FORM_MIDDLE_NEW, "form middle"),
        # 8. allowTokens field -> pointer to the two modules that now own
        #    the address and the token toggles (token names shown the way
        #    the cashier shows them: USDT0 displayed as USDT)
        ('(0,q.jsx)(j,{control:n.control,name:`allowTokens`,render:({field:e})=>(0,q.jsxs)(k,{children:[(0,q.jsx)(P,{children:"Allowed tokens"}),(0,q.jsx)(N,{children:(0,q.jsx)(R,{placeholder:`USDC,USDT0,USDG`,...e})}),(0,q.jsx)(F,{children:"Comma-separated token symbols, e.g. USDC,USDT0,USDG"}),(0,q.jsx)(I,{})]})})',
         '(0,q.jsxs)(`div`,{className:`rounded-md border border-dashed p-3 text-sm`,children:[(0,q.jsx)(`p`,{className:`font-medium`,children:"收款地址与代币开关"}),(0,q.jsx)(`p`,{className:`mt-2 text-muted-foreground leading-relaxed`,children:"收款地址统一在「地址管理」维护（新增网络 xlayer 的地址并启用后生效，本页不再单独保存）；可收代币统一在「链与代币」的 X Layer 分组下逐个开关（USDC / USDT / USDG），开关即时作用于收银台。"})]})',
         "allowTokens -> pointer card"),
        # 9. redesigned guide card as first form child (review points 1+4)
        ("(0,q.jsxs)(`form`,{className:`space-y-6`,onSubmit:n.handleSubmit(i),children:[(0,q.jsx)(j,{control:n.control,name:`enabled`",
         "(0,q.jsxs)(`form`,{className:`space-y-6`,onSubmit:n.handleSubmit(i),children:[" + GUIDE + ",(0,q.jsx)(j,{control:n.control,name:`enabled`",
         "guide card"),
        # 10. page header
        ('(0,q.jsx)(B,{description:"Configure OKX Onchain OS Payment (X Layer) hosted checkout.",title:"X Layer",variant:`section`,children:(0,q.jsx)(Y,{})})',
         '(0,q.jsx)(B,{description:"对接 OKX Onchain OS Payment（X Layer）托管收银台：本页维护 API 凭证与超时；收款地址在「地址管理」维护，代币开关在「链与代币」维护。",title:"OKX OnchainOS 配置",variant:`section`,children:(0,q.jsx)(Y,{})})',
         "page header"),
        # 11. save button + toast (badge script set these literals)
        ('(0,q.jsx)(w,{disabled:t.isPending||e.isLoading,type:`submit`,children:"保存 X Layer 配置"})',
         '(0,q.jsx)(w,{disabled:t.isPending||e.isLoading,type:`submit`,children:"保存 OKX OnchainOS 配置"})',
         "save button"),
        ('}],"X Layer 配置已保存"),await e.refetch()',
         '}],"OKX OnchainOS 配置已保存"),await e.refetch()',
         "save toast"),
    ]
    out = t
    for old, new, label in reps:
        out = replace_once(out, old, new, label)
    # nothing of the old surface may survive; the 2nd review also bans
    # every error-code mention and the amber warning styling; the 3rd
    # review bans the 2nd-review divider + standalone Passphrase heading;
    # the 4th review bans the Passphrase note sentence and every trace of
    # the api_url field/key (page no longer reads or writes it)
    for bad in ("toAddress", "allowTokens", "xlayer.to_address", "xlayer.allow_tokens",
                "xlayer.return_url", "returnUrl", "return_url",
                "apiUrl", "api_url",
                "丢失后只能删除 Key 重建", "密码短语",
                "Enable X Layer", "Allowed tokens", "Receiving address",
                "保存 X Layer 配置", "X Layer 配置已保存",
                "Timeout (seconds)", "API URL",
                "50125", "50111", "61009", "常见报错", "amber",
                "Passphrase（密码）", "(0,q.jsx)(`div`,{className:`border-t`})",
                # missing-comma guards: adjacent jsx() calls inside a
                # children array must be comma-separated (a direct `)(0,q.jsx`
                # junction would chain the calls and still pass node --check)
                ")(0,q.jsx", "](0,q.jsx"):
        if bad in out:
            raise SystemExit(f"FAIL chunk: {bad!r} still present")
    for good in ("OKX OnchainOS 配置", "dev-portal",
                 "API 凭证", "API 地址", "https://web3.okx.com（固定）",
                 "接口超时（秒）", "收款地址与代币开关", "settled",
                 "text-base font-semibold"):
        if good not in out:
            raise SystemExit(f"FAIL chunk: {good!r} missing")
    p.write_text(out)
    print(f"patched xlayer-7Kx2Qw9Z.js ({len(out)} bytes)")


def patch_file(path, reps):
    text = path.read_text()
    for old, new, label in reps:
        text = replace_once(text, old, new, label)
    path.write_text(text)
    print(f"patched {path.name}")


def main():
    patch_chunk()
    patch_file(ASSETS / "route-BkjShyH8.js", [
        ('{title:"X Layer",href:`/settings/xlayer`,icon:(0,S.jsx)(u,{size:18})}',
         '{title:"OKX OnchainOS 配置",href:`/settings/xlayer`,icon:(0,S.jsx)(u,{size:18})}',
         "sidebar title"),
    ])
    patch_file(ASSETS / "search-provider-DPXFLA4q.js", [
        ('{title:"X Layer",url:`/settings/xlayer`}',
         '{title:"OKX OnchainOS 配置",url:`/settings/xlayer`}',
         "palette title"),
    ])
    for f in ["xlayer-7Kx2Qw9Z.js", "route-BkjShyH8.js", "search-provider-DPXFLA4q.js"]:
        r = subprocess.run(["node", "--check", str(ASSETS / f)],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(r.stdout, r.stderr)
            raise SystemExit(f"FAIL node --check {f}")
        print(f"node --check ok: {f}")
    print("ALL OK")


if __name__ == "__main__":
    main()
