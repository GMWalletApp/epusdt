#!/usr/bin/env python3
"""Add a gas-free badge next to X Layer, and fix the X Layer admin page's
OkPay copy-paste labels.

1. labels-BwEfuu7e.js -- function r (network label used by the cashier
   network select trigger + dropdown options, and by admin pages):
   append a small "免 Gas" pill after the name when network == xlayer.
   Only Tailwind classes already present in the built index-*.css are
   used (bg-emerald-500/10, text-emerald-700, dark:text-emerald-400,
   px-1.5, py-0.5, text-xs, rounded-full, font-semibold, shrink-0);
   Tailwind only ships classes seen at build time, so a brand-new
   utility would render unstyled.
2. _trade_id-B2s-YYlo.js -- function hi (network pill shown in the
   cashier payment step): same badge.
3. xlayer-7Kx2Qw9Z.js -- the save button and save toast still reuse the
   OkPay i18n strings ("保存 OkPay 配置" / "OkPay 配置已保存"); replace
   with X Layer literals.

Re-run is only valid on a pristine tree (see build-xlayer-ui.py).
"""
import subprocess
from pathlib import Path

ASSETS = Path(__file__).resolve().parent / "src" / "www" / "assets"
BADGE = ("(0,n.jsx)(`span`,{className:`shrink-0 rounded-full "
         "bg-emerald-500/10 px-1 py-0.5 text-xs font-semibold "
         "text-emerald-700 dark:text-emerald-400`,children:`免Gas`})")


def replace_once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"FAIL {label}: expected 1 occurrence, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def main():
    # 1. network label component (cashier select + options)
    p = ASSETS / "labels-BwEfuu7e.js"
    t = p.read_text()
    old_r = ("function r({displayName:e,network:t}){return(0,n.jsx)(i,"
             "{label:e?.trim()||t,network:t,symbol:``})}")
    # hideIcon: the closed select trigger renders the plain name (no chain
    # icon) so "X Layer" + badge fits the narrow column; the dropdown
    # options keep the icon. Badge is compact: px-1, text "免Gas".
    new_r = ("function r({displayName:e,hideIcon:a,network:t}){return(0,n.jsxs)(`span`,"
             "{className:`flex min-w-0 items-center gap-1`,children:["
             "a?(0,n.jsx)(`span`,{className:`truncate`,children:e?.trim()||t})"
             ":(0,n.jsx)(i,{label:e?.trim()||t,network:t,symbol:``}),"
             "String(t||``).toLowerCase()===\"xlayer\"?"
             + BADGE +
             ":null]})}")
    t = replace_once(t, old_r, new_r, "labels r()")
    p.write_text(t)
    print("patched labels-BwEfuu7e.js")

    # 2. cashier payment-step network pill
    p = ASSETS / "_trade_id-B2s-YYlo.js"
    t = p.read_text()
    old_hi = ("(0,G.jsx)(`span`,{className:`truncate font-semibold text-xs`,"
              "children:t?.trim()||n})]})}")
    badge_g = BADGE.replace("(0,n.jsx)", "(0,G.jsx)")
    new_hi = ("(0,G.jsx)(`span`,{className:`truncate font-semibold text-xs`,"
              "children:t?.trim()||n}),String(n||``).toLowerCase()===\"xlayer\"?"
              + badge_g + ":null]})}")
    t = replace_once(t, old_hi, new_hi, "cashier hi()")

    # 2b. closed network selector: pass hideIcon so the name is not
    # truncated by the chain icon + badge; narrow the token column to
    # w-32 so the network column gets the remaining width.
    t = replace_once(
        t,
        "(0,G.jsx)(pe,{placeholder:(0,G.jsx)(Re,{displayName:_?.displayName,network:s})})",
        "(0,G.jsx)(pe,{placeholder:(0,G.jsx)(Re,{displayName:_?.displayName,hideIcon:!0,network:s})})",
        "cashier trigger hideIcon",
    )
    t = replace_once(
        t,
        "(0,G.jsxs)(`div`,{className:`min-w-0 flex-1`,children:[(0,G.jsx)(`p`,{className:`mb-1.5 font-semibold text-muted-foreground text-xs uppercase tracking-wider`,children:ne()})",
        "(0,G.jsxs)(`div`,{className:`w-32 shrink-0`,children:[(0,G.jsx)(`p`,{className:`mb-1.5 font-semibold text-muted-foreground text-xs uppercase tracking-wider`,children:ne()})",
        "cashier token column w-32",
    )
    p.write_text(t)
    print("patched _trade_id-B2s-YYlo.js")

    # 3. X Layer admin page: save button + save toast wording
    p = ASSETS / "xlayer-7Kx2Qw9Z.js"
    t = p.read_text()
    t = replace_once(
        t,
        "(0,q.jsx)(w,{disabled:t.isPending||e.isLoading,type:`submit`,children:_()})",
        "(0,q.jsx)(w,{disabled:t.isPending||e.isLoading,type:`submit`,children:\"保存 X Layer 配置\"})",
        "xlayer save button",
    )
    t = replace_once(t, "}],h()),await e.refetch()",
                    "}],\"X Layer 配置已保存\"),await e.refetch()",
                    "xlayer save toast")
    p.write_text(t)
    print("patched xlayer-7Kx2Qw9Z.js")

    for f in ["labels-BwEfuu7e.js", "_trade_id-B2s-YYlo.js", "xlayer-7Kx2Qw9Z.js"]:
        r = subprocess.run(["node", "--check", str(ASSETS / f)],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(r.stdout, r.stderr)
            raise SystemExit(f"FAIL node --check {f}")
        print(f"node --check ok: {f}")
    print("ALL OK")


if __name__ == "__main__":
    main()
