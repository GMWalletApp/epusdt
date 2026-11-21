#!/usr/bin/env python3
"""Show USDT0 as USDT in the cashier, and give USDT its correct logo.

User-facing problem (2026-11-20): on the X Layer cashier the token list
shows "USDT0" with a grey placeholder circle, while USDC shows its real
blue $ logo. The user wants (1) the name shown as "USDT", (2) the real
Tether USDT logo, rendered the same way as the other tokens.

Display-only change. The identifier "USDT0" must stay everywhere it
matters: config xlayer.allow_tokens, POST /pay/switch-network token
value, xlayer_service.go (token sent to the OKX create-order API) and
the database. Only what the buyer sees changes.

Three patches, all in the built SPA under src/www/assets:
1. crypto-icon-yn0aBrU6.js -- the icon component resolves a token logo
   from Trust Wallet assets via its `u` map (symbol -> {network,
   contractAddress}); USDT is in the map (ethereum
   0xdAC17F958D2ee523a2206206994597C13D831ec7) but USDT0 is not, so it
   falls back to a grey circle. Add a USDT0 entry pointing at the same
   USDT contract. (This chunk is shared with admin pages and is NOT
   renamed by rename-xlayer-chunks.py; adding the entry is harmless.)
   Same fix for USDG (2026-11-20): no USDG entry in `u`, so the X Layer
   USDG option renders as a grey circle too. Add USDG -> the Paxos USDG
   ethereum contract 0xe343167631d89B6Ffc58B88d6b7fB0228795491D (the
   Trust Wallet assets repo has its logo there; the X Layer-side USDG
   contract is a different address and is NOT used for the icon, same
   convention as USDT/USDC).
2. labels-BwEfuu7e.js -- function i renders the token dropdown options
   and the closed trigger (icon + label text). Normalize the label
   USDT0 -> USDT for display, and the icon symbol the same way.
3. _trade_id-B2s-YYlo.js -- function Ei renders the payment-step amount
   as `actual_amount + token` ("0.01 USDT0"). Map the token to USDT
   for display only.

Admin displays (2026-11-20 review: everywhere in the admin that SHOWS
the USDT0 token must say USDT, same as the cashier; identifiers keep
USDT0 -- chain_tokens.symbol, xlayer.allow_tokens, switch-network,
OKX create-order params, and the api_keys token whitelist badges on
payment-setup, which merchants echo back in API calls):
4. chains-tUDg5ohy.js -- the token table's symbol cell and the edit
   dialog's (disabled) symbol input map USDT0 -> USDT.
5. orders-BnOHTHOA.js -- the actual_amount table cell and the order
   drawer row render `amount token`; map the token for display.
6. dashboard-Cw_CStip.js -- the recent-orders list renders
   `actual_amount token`; map likewise.

Re-run is only valid on a pristine tree (see build-xlayer-ui.py).
Run AFTER build-xlayer-redirect.py, BEFORE rename-xlayer-chunks.py.
"""
import subprocess
from pathlib import Path

ASSETS = Path(__file__).resolve().parent / "src" / "www" / "assets"
USDT_CONTRACT = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
USDG_CONTRACT = "0xe343167631d89B6Ffc58B88d6b7fB0228795491D"


def replace_once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"FAIL {label}: expected 1 occurrence, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def main():
    # 1. crypto-icon: USDT0 resolves to the Tether USDT logo.
    p = ASSETS / "crypto-icon-yn0aBrU6.js"
    t = p.read_text()
    old_u = ("USDT:{network:`ethereum`,contractAddress:`" + USDT_CONTRACT + "`},TRX:")
    new_u = ("USDT:{network:`ethereum`,contractAddress:`" + USDT_CONTRACT + "`},"
             "USDT0:{network:`ethereum`,contractAddress:`" + USDT_CONTRACT + "`},"
             "USDG:{network:`ethereum`,contractAddress:`" + USDG_CONTRACT + "`},TRX:")
    t = replace_once(t, old_u, new_u, "crypto-icon USDT0/USDG map")
    p.write_text(t)
    print("patched crypto-icon-yn0aBrU6.js")

    # 2. token label: display USDT0 as USDT (dropdown options + trigger),
    # and look the icon up as USDT too.
    p = ASSETS / "labels-BwEfuu7e.js"
    t = p.read_text()
    old_i = ('function i({contractAddress:e,label:r,network:i,networkIndependent:a,symbol:o}){return(0,n.jsxs')
    new_i = ('function i({contractAddress:e,label:r,network:i,networkIndependent:a,symbol:o}){'
             'r=r.replace(/^USDT0(?= \u00b7 |$)/,"USDT"),o=o==="USDT0"?"USDT":o;return(0,n.jsxs')
    t = replace_once(t, old_i, new_i, "labels i() display name")
    p.write_text(t)
    print("patched labels-BwEfuu7e.js")

    # 3. payment-step amount: "0.01 USDT0" -> "0.01 USDT" (display only).
    p = ASSETS / "_trade_id-B2s-YYlo.js"
    t = p.read_text()
    old_ei = ('function Ei(e){let t=e?.actual_amount??e?.amount,n=e?.actual_amount==null?e?.currency:e?.token;'
              'return t==null?`--`:Di(t,n)}')
    new_ei = ('function Ei(e){let t=e?.actual_amount??e?.amount,n=e?.actual_amount==null?e?.currency:e?.token;'
              'n=n==="USDT0"?"USDT":n;return t==null?`--`:Di(t,n)}')
    t = replace_once(t, old_ei, new_ei, "cashier Ei() display token")
    p.write_text(t)
    print("patched _trade_id-B2s-YYlo.js")

    # 4. chains page (admin): symbol column cell + edit-dialog symbol input
    #    (disabled on edit, so display-only).
    p = ASSETS / "chains-tUDg5ohy.js"
    t = p.read_text()
    t = replace_once(
        t,
        "cell:({row:e})=>(0,Z.jsx)(`div`,{className:`font-medium`,children:e.original.symbol??`-`}),enableHiding:!1,meta:{label:p(),skeleton:`short`}",
        "cell:({row:e})=>(0,Z.jsx)(`div`,{className:`font-medium`,children:e.original.symbol===`USDT0`?`USDT`:(e.original.symbol??`-`)}),enableHiding:!1,meta:{label:p(),skeleton:`short`}",
        "chains symbol cell display name",
    )
    t = replace_once(
        t,
        "symbol:n?.symbol??``,contract_address:n?.contract_address??``",
        "symbol:n?.symbol===`USDT0`?`USDT`:(n?.symbol??``),contract_address:n?.contract_address??``",
        "chains edit dialog symbol display name",
    )
    p.write_text(t)
    print("patched chains-tUDg5ohy.js")

    # 5. admin orders page: actual_amount cell + order drawer show
    #    "amount token"; map the token for display.
    p = ASSETS / "orders-BnOHTHOA.js"
    t = p.read_text()
    t = replace_once(
        t,
        "cell:({row:e})=>`${X(e.original.actual_amount,6)} ${e.original.token||``}`",
        "cell:({row:e})=>`${X(e.original.actual_amount,6)} ${e.original.token===`USDT0`?`USDT`:e.original.token||``}`",
        "orders table amount token display",
    )
    t = replace_once(
        t,
        "`${X(n?.actual_amount,6)} ${n?.token??``}`.trim()||`-`",
        "`${X(n?.actual_amount,6)} ${n?.token===`USDT0`?`USDT`:n?.token??``}`.trim()||`-`",
        "orders drawer token display",
    )
    p.write_text(t)
    print("patched orders-BnOHTHOA.js")

    # 6. admin dashboard recent orders: "amount token" label, same mapping.
    p = ASSETS / "dashboard-Cw_CStip.js"
    t = p.read_text()
    t = replace_once(
        t,
        "children:[Wt(e.actual_amount,6),` `,e.token||``]",
        "children:[Wt(e.actual_amount,6),` `,e.token===`USDT0`?`USDT`:e.token||``]",
        "dashboard recent orders token display",
    )
    p.write_text(t)
    print("patched dashboard-Cw_CStip.js")

    for f in ["crypto-icon-yn0aBrU6.js", "labels-BwEfuu7e.js", "_trade_id-B2s-YYlo.js",
              "chains-tUDg5ohy.js", "orders-BnOHTHOA.js", "dashboard-Cw_CStip.js"]:
        r = subprocess.run(["node", "--check", str(ASSETS / f)],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(r.stdout, r.stderr)
            raise SystemExit(f"FAIL node --check {f}")
        print(f"node --check ok: {f}")
    print("ALL OK")


if __name__ == "__main__":
    main()
