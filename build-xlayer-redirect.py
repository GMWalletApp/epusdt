#!/usr/bin/env python3
"""Skip the intermediate hosted-payment page for X Layer: after the buyer
clicks confirm on the cashier, jump straight to the OKX hosted checkout
instead of showing the "opening OkPay window" step with a button.

The OKPay flow opens the checkout in a popup (window.open inside the
click gesture, then the popup is navigated to payment_url). X Layer never
went through that path: the switch-network success handler only opened
the popup for okpay, so an X Layer order always landed on the
intermediate step and waited for a manual button click (and a popup
opened outside the user gesture would be blocked anyway).

v1 (2026-11-18) added two patches, both in _trade_id-B2s-YYlo.js, both
X Layer-only (OKPay behaviour is untouched):
  1. Switch success (Oe): after the okpay branch, when the switched
     network is xlayer and the response carries a payment_url, do a
     same-tab redirect (window.location.href) instead of falling through
     to the intermediate step.
  2. Hosted-payment step component (Vt): a useEffect that redirects to
     the payment_url on mount / when it arrives, when the order is
     xlayer. This covers refreshes and direct opens of a cashier link
     whose order already has a payment_url.

v2 (2026-11-20): v1 was NOT enough -- the user still saw the
intermediate page flash before the jump to OKX. Root causes:
  (a) Patch 1 ran AFTER f({...x, ...a}) updated the order state, so
      React re-rendered to the payment step (rendering Vt, the
      intermediate page) before window.location.href took effect; while
      the OKX page was still loading, the user stared at the
      intermediate page.
  (b) Patch 2 only fires AFTER the intermediate page mounts and paints
      (useEffect runs post-paint), so a flash is structural, not a bug
      in the patch.
  (c) Refresh / direct-open of an already-switched order never runs
      patch 1 at all, only patch 2.
Fix in v2, still X Layer-only:
  1. Oe: move the xlayer redirect BEFORE any setState call. If the
     switch response is xlayer with a payment_url, navigate immediately
     and return without calling f()/v() -- no re-render to the payment
     step ever happens.
  2. Zt (the panel dispatcher): in the `payment` case, if the order is
     xlayer with a payment_url, navigate immediately during render and
     return a minimal "jumping to OKX" placeholder instead of Vt. Vt
     is never rendered for xlayer, so the intermediate page (its card,
     its "opening OkPay window" copy, its button, the payment_url text)
     never paints. This also covers refresh / direct-open, which only
     ever reaches Zt.
  3. Vt's useEffect from v1 is kept as a last-resort fallback only.

Run order on a pristine tree (git checkout -- src/www/assets first):
build-xlayer-ui.py, build-xlayer-badge.py, then this script.
"""
import subprocess
from pathlib import Path

ASSETS = Path(__file__).resolve().parent / "src" / "www" / "assets"


def replace_once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"FAIL {label}: expected 1 occurrence, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def main():
    p = ASSETS / "_trade_id-B2s-YYlo.js"
    t = p.read_text()

    # 1. Switch success: redirect straight to OKX for xlayer, BEFORE any
    #    setState (f()/v()) so React never re-renders to the payment step.
    t = replace_once(
        t,
        "let a=Qe(i);if(f({...x,...a,is_selected:a.is_selected??!0,network:a.network??e.network,token:a.token??e.token}),v(!1),n&&a.payment_url){De(a.payment_url,t);return}t?.close(),a.trade_id&&a.trade_id!==x.trade_id&&r({params:{trade_id:a.trade_id},replace:!0,",
        "let a=Qe(i);if(H(e.network,`xlayer`)&&a.payment_url){window.location.href=a.payment_url;return}if(f({...x,...a,is_selected:a.is_selected??!0,network:a.network??e.network,token:a.token??e.token}),v(!1),n&&a.payment_url){De(a.payment_url,t);return}t?.close(),a.trade_id&&a.trade_id!==x.trade_id&&r({params:{trade_id:a.trade_id},replace:!0,",
        "switch xlayer redirect before setState",
    )

    # 2. Hosted-payment step: auto-redirect on mount when xlayer (fallback).
    t = replace_once(
        t,
        "}){let[b,x]=(0,U.useState)(!1),S=i?.receive_address??``,C=i?.payment_url;return c===`okpay`",
        "}){let[b,x]=(0,U.useState)(!1),S=i?.receive_address??``,C=i?.payment_url;(0,U.useEffect)(()=>{if(C&&String(i?.network??``)===`xlayer`)window.location.href=C},[C]);return c===`okpay`",
        "step xlayer auto-redirect",
    )

    # 3. Panel dispatcher: never render Vt for xlayer with a payment_url.
    #    Navigate during render and show only a minimal placeholder.
    t = replace_once(
        t,
        "case`payment`:return(0,G.jsx)(Vt,{onChangePaymentOption:n,",
        "case`payment`:if(String(p?.network??``).toLowerCase()===`xlayer`&&p?.payment_url){window.location.href=p.payment_url;return(0,G.jsx)(`div`,{className:`w-full pb-4 text-center text-sm text-muted-foreground`,children:`正在跳转 OKX…`})}return(0,G.jsx)(Vt,{onChangePaymentOption:n,",
        "dispatcher xlayer intercept",
    )
    p.write_text(t)
    print("patched _trade_id-B2s-YYlo.js (v2)")

    r = subprocess.run(["node", "--check", str(p)], capture_output=True, text=True)
    if r.returncode != 0:
        print(r.stdout, r.stderr)
        raise SystemExit("FAIL node --check _trade_id-B2s-YYlo.js")
    print("node --check ok: _trade_id-B2s-YYlo.js")
    print("ALL OK")


if __name__ == "__main__":
    main()
