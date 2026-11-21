#!/usr/bin/env python3
"""Cache-bust the X Layer cashier path by renaming chunks.

Why (2026-11-18): patched chunks keep their Vite-hashed filenames, so a
browser that cached the ORIGINAL (Nov 2) copies treats them as fresh for
up to 24h (heuristic caching: freshness ~= 10% of age, capped at 24h) and
never revalidates -- not even after the server started sending
Cache-Control: no-cache, which only affects responses served after it
was deployed and cannot evict copies already stored. The user kept
seeing the pre-badge / pre-redirect code on different devices for this
reason. New filenames have no cached copy anywhere, so every browser
must fetch the new code on its next navigation (index.html itself is
re-extracted on every container start, so its Last-Modified is always
recent and it revalidates within minutes).

Run AFTER build-xlayer-ui.py, build-xlayer-badge.py and
build-xlayer-redirect.py, BEFORE the Go build. The old-named files are
KEPT (with patched content) as a fallback for any stale importer that
still references them; the fresh chain (new index.html -> new entry ->
new router -> new wrapper -> new leaf/labels) never touches them.

Rebuild sequence (standing convention):
  git checkout -- src/www/assets
  python3 build-xlayer-ui.py
  python3 build-xlayer-badge.py
  python3 build-xlayer-redirect.py
  python3 rename-xlayer-chunks.py   <- this script
  go build (from src/) -> push -> docker cp -> restart
"""
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent / "src" / "www"
RENAMES = {
    "index-DMvm0YrM.js": "index-XL2.js",
    "router-aZWjwYfd.js": "router-XL2.js",
    "_trade_id-ygb9_KBy.js": "_trade_id-ygb9-XL2.js",
    "_trade_id-B2s-YYlo.js": "_trade_id-B2s-XL2.js",
    "labels-BwEfuu7e.js": "labels-XL2.js",
    # 2026-11-20: the chains chunk gets the USDT0->USDT display mapping;
    # it is an original-build chunk (old Last-Modified), so without a new
    # name a browser could keep the cached USDT0 copy for up to 24h.
    "chains-tUDg5ohy.js": "chains-XL2.js",
    # 2026-11-20 (2nd review, restructured again in the 3rd review): the
    # OKX OnchainOS settings page gets its error-code block removed and
    # the credential fields regrouped (all three in ONE 「API 凭证」
    # card), but the chunk has kept its Vite-hashed name since it first
    # shipped, so a browser could serve a stale pre-redesign copy for up
    # to 24h and the user would still see the old field grouping.
    # Rename it like the other patched chunks (only router-* imports it;
    # the /settings/xlayer route path itself is untouched).
    "xlayer-7Kx2Qw9Z.js": "xlayer-XL2.js",
}


def main():
    assets = ROOT / "assets"
    for old, new in RENAMES.items():
        src = assets / old
        dst = assets / new
        if not src.exists():
            sys.exit(f"missing {old}")
        shutil.copyfile(src, dst)
        print(f"copied {old} -> {new}")
    # Global replace of old names -> new names in every text file under www/.
    files = [ROOT / "index.html"] + sorted(assets.glob("*.js")) + sorted(assets.glob("*.css"))
    for path in files:
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        orig = text
        for old, new in RENAMES.items():
            text = text.replace(old, new)
        if text != orig:
            path.write_text(text, encoding="utf-8")
            print(f"rewrote refs in {path.relative_to(ROOT)}")
    # Verify: new files exist and contain the redirect + badge; old names
    # gone from index.html.
    idx = (ROOT / "index.html").read_text(encoding="utf-8")
    for old in RENAMES:
        assert old not in idx, f"{old} still referenced in index.html"
    leaf = (assets / RENAMES["_trade_id-B2s-YYlo.js"]).read_text(encoding="utf-8")
    assert "window.location.href=a.payment_url" in leaf, "redirect patch missing in renamed leaf"
    assert "window.location.href=C" in leaf, "redirect effect missing in renamed leaf"
    assert "免Gas" in leaf, "badge missing in renamed leaf"
    labels = (assets / RENAMES["labels-BwEfuu7e.js"]).read_text(encoding="utf-8")
    assert "免Gas" in labels, "badge missing in renamed labels"
    chains = (assets / RENAMES["chains-tUDg5ohy.js"]).read_text(encoding="utf-8")
    assert "e.original.symbol===`USDT0`?`USDT`" in chains, "USDT display mapping missing in renamed chains"
    settings = (assets / RENAMES["xlayer-7Kx2Qw9Z.js"]).read_text(encoding="utf-8")
    assert "API 凭证" in settings, "credentials card heading missing in renamed settings chunk"
    assert "text-base font-semibold" in settings, \
        "credentials card heading style missing in renamed settings chunk"
    assert settings.count("(0,q.jsx)(`div`,{className:`border-t`})") == 0, \
        "the 2nd-review divider must be gone (3rd review: one card, no divider)"
    assert "Passphrase（密码）" not in settings, \
        "standalone passphrase section must be gone (3rd review)"
    # 2026-11-21 4th review: the Passphrase note sentence is deleted and
    # API 地址 becomes static text -- no apiUrl field/key anywhere.
    assert "丢失后只能删除 Key 重建" not in settings, \
        "passphrase note must be gone (4th review)"
    assert "apiUrl" not in settings and "api_url" not in settings, \
        "api_url field/key must be gone (4th review: fixed backend base URL)"
    assert "API 地址" in settings and "https://web3.okx.com（固定）" in settings, \
        "static API 地址 text missing in renamed settings chunk"
    for banned in ("50125", "50111", "61009", "常见报错", "amber"):
        assert banned not in settings, f"{banned!r} still present in renamed settings chunk"
    print("OK: renames applied, redirect+badge present in new files")


if __name__ == "__main__":
    main()
