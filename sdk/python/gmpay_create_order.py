#!/usr/bin/env python3
"""Create a GMPay test order with an HMAC-SHA256 signature.

Example:
    python3 gmpay_create_order.py \
        --api-url https://pay.example.com/payments/gmpay/v1/order/create-transaction \
        --pid 1000 \
        --notify-url https://merchant.example.com/payment/notify \
        --redirect-url https://merchant.example.com/payment/return \
        --token usdt \
        --network binance

The secret key is read from GMPAY_SECRET_KEY or requested without terminal echo.
Only Python's standard library is required.
"""

import argparse
import getpass
import hashlib
import hmac
import json
import math
import os
import sys
import time
import urllib.error
import urllib.request
from decimal import Decimal


def canonical_value(value):
    """Format a value like Go's strconv.FormatFloat(value, 'f', -1, 64)."""
    if isinstance(value, float):
        if not math.isfinite(value):
            raise ValueError("Signed parameters cannot contain NaN or Infinity")
        if value.is_integer():
            return str(int(value))
        text = repr(value)
        if "e" in text.lower():
            text = format(Decimal(text), "f")
        return text
    if isinstance(value, bool):
        raise TypeError("Do not use booleans in signed parameters")
    return str(value)


def make_signature(params, secret_key):
    """Return the canonical parameter string and its HMAC-SHA256 signature."""
    pairs = []
    for key, value in params.items():
        if key == "signature" or value is None:
            continue
        text = canonical_value(value)
        if text == "":
            continue
        pairs.append(f"{key}={text}")

    canonical = "&".join(sorted(pairs))
    signature = hmac.new(
        secret_key.encode("utf-8"),
        canonical.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()
    return canonical, signature


def positive_amount(value):
    """Parse and validate a positive order amount."""
    try:
        amount = float(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("amount must be a number") from exc
    if not math.isfinite(amount) or amount <= 0.01:
        raise argparse.ArgumentTypeError("amount must be greater than 0.01")
    return amount


def parse_args():
    """Parse command-line options without embedding merchant configuration."""
    parser = argparse.ArgumentParser(
        description="Create a GMPay order using the HMAC-SHA256 signature scheme."
    )
    parser.add_argument("--api-url", required=True, help="GMPay create-order endpoint")
    parser.add_argument("--pid", required=True, help="Merchant PID")
    parser.add_argument("--notify-url", required=True, help="Public payment callback URL")
    parser.add_argument("--redirect-url", default="", help="Browser return URL")
    parser.add_argument("--token", default="usdt", help="Payment token (default: usdt)")
    parser.add_argument("--network", default="tron", help="Payment network (default: tron)")
    parser.add_argument("--currency", default="cny", help="Fiat currency (default: cny)")
    parser.add_argument("--amount", type=positive_amount, default=1, help="Order amount")
    parser.add_argument("--name", default="GMPay Python signature test", help="Order name")
    parser.add_argument("--order-id", help="Unique merchant order ID; generated when omitted")
    return parser.parse_args()


def read_secret_key():
    """Read the key from the environment or securely prompt for it."""
    secret_key = os.environ.get("GMPAY_SECRET_KEY", "")
    if not secret_key:
        secret_key = getpass.getpass("GMPay secret key: ")
    if not secret_key:
        raise ValueError("The GMPay secret key cannot be empty")
    return secret_key


def main():
    args = parse_args()
    try:
        secret_key = read_secret_key()
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    order_id = args.order_id or f"PYTEST{int(time.time() * 1000)}"
    if len(order_id) > 32:
        print("Order ID cannot exceed 32 characters", file=sys.stderr)
        return 2

    payload = {
        "pid": args.pid,
        "order_id": order_id,
        "currency": args.currency,
        "token": args.token,
        "network": args.network,
        "amount": args.amount,
        "notify_url": args.notify_url,
        "redirect_url": args.redirect_url,
        "name": args.name,
    }

    canonical, payload["signature"] = make_signature(payload, secret_key)
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        args.api_url,
        data=body,
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "gmpay-python-signature-test/1.0",
        },
        method="POST",
    )

    print(f"API URL: {args.api_url}")
    print(f"Order ID: {order_id}")
    print(f"Canonical string: {canonical}")
    print(f"HMAC-SHA256: {payload['signature']}")

    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            status = response.status
            raw = response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        status = exc.code
        raw = exc.read().decode("utf-8", errors="replace")
    except urllib.error.URLError as exc:
        print(f"Request failed: {exc.reason}", file=sys.stderr)
        return 1
    except TimeoutError:
        print("Request failed: timed out", file=sys.stderr)
        return 1

    print(f"HTTP status: {status}")
    try:
        result = json.loads(raw)
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except json.JSONDecodeError:
        print(raw)
        return 1

    if isinstance(result, dict) and status == 200 and result.get("status_code") == 200:
        print("Order created successfully")
        return 0

    print("Order creation failed; inspect status_code and message above", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
