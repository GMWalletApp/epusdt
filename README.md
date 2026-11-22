<div align="center">

<img src="./src/www/images/logo.png" alt="EPUSDT" width="100">

# EPUSDT

**A self-hosted crypto payment gateway for merchants**

<p>English · <a href="./README.zh_CN.md">简体中文</a> · <a href="https://epusdt.com/">Visit the official site for other languages</a></p>

<p>
  <a href="https://epusdt.com/api/payment">Merchant API</a> ·
  <a href="https://epusdt.com/guide/intro">Documentation</a> ·
  <a href="https://t.me/epusdt">Telegram channel</a> ·
  <a href="https://t.me/epusdt_group">Telegram group</a>
</p>

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GPLv3-brightgreen" alt="GPLv3"></a>
  <a href="https://github.com/GMWalletApp/epusdt/releases/latest"><img src="https://img.shields.io/github/v/release/GMWalletApp/epusdt?color=brightgreen" alt="GitHub release"></a>
  <a href="https://hub.docker.com/r/gmwallet/epusdt"><img src="https://img.shields.io/badge/Docker-gmwallet%2Fepusdt-2496ED?logo=docker&logoColor=white" alt="Docker Hub"></a>
</p>

</div>

## Development

EPUSDT is written in Go and uses SQLite. Merchants create orders through GMPay or the EPay-compatible API. The embedded ChainScanner tracks receiving addresses. EPUSDT verifies incoming payments and sends signed callbacks to merchants.

From the repository root, use Go 1.25 or later to test and build the gateway.

```sh
cd src
go test ./...
make build
```

The binary is written to `src/bin/epusdt`.

| Directory | Contents |
| --- | --- |
| [src](src/) | HTTP routes, order state, payment verification and callbacks |
| [chainscanner](chainscanner/) | Pinned scanner source, updated with the [sync script](scripts/sync-chainscanner) |
| [sdk/python](sdk/python/) | Python example for signing and creating orders |
| [src/docs](src/docs/) | Generated Swagger specification and API error codes |

[Development standards](DEVELOPMENT_STANDARD.md) · [Code review](REVIEW.md) · [Release process](RELEASING.md)

## Merchant documentation

[Supported networks, tokens and full contract addresses](https://epusdt.com/guide/intro#networks) · [Docker installation and upgrades](https://epusdt.com/guide/installation/docker) · [Merchant API](https://epusdt.com/api/payment) · [EPay integration](https://epusdt.com/guide/integration/epay)

A7A5 is available on these mainnets when enabled by the merchant.

| Network | A7A5 contract | Decimals |
| --- | --- | ---: |
| TRON (TRC-20) | `TLeVfrdym8RoJreJ23dAGyfJDygRtiWKBZ` | 6 |
| Ethereum (ERC-20) | `0x6fA0BE17e4beA2fCfA22ef89BF8ac9aab0AB0fc9` | 6 |

## Deploy or migrate to EPUSDT

Copy the prompt below and paste it into Codex, Claude Code, Cursor or OpenCode.

```text
Read https://epusdt.com/agent-setup/prompt.md. Inspect my target environment first, then deploy, upgrade or migrate to EPUSDT (GMPay) using Docker. Preserve existing data. For a new instance, open the setup wizard and let me complete password setup myself.
```

[Read the deployment checklist](https://epusdt.com/agent-setup/prompt.md)

## Audit and license

[VectorBits audit report (May 2026)](https://github.com/VectorBits/audit/blob/main/epusdt-secure-audit-report-2026-05-14.pdf) · [GPLv3 license](LICENSE)

<details>
<summary>Disclaimer and Terms of Use</summary>

EPusdt is developed and published by Good Morning Technology, LLC as free, open-source, non-profit, and non-custodial software for learning, research, and technical communication. Nothing in this project constitutes investment, financial, legal, tax, compliance, or any other professional advice, nor should it be interpreted as a guarantee regarding any asset, transaction outcome, revenue, fund safety, technical availability, or fitness for a particular purpose.

Good Morning Technology, LLC is an entity organized under United States law and will comply with applicable legal and regulatory obligations as required. Under U.S. regulatory frameworks, whether virtual currencies, digital assets, or related technical services constitute money transmission, a money services business (MSB), or another regulated activity generally depends on the specific business model, including whether the operator receives, holds, controls, or transmits funds or value, provides custody on behalf of users, or engages in exchange, payment, clearing, settlement, or other intermediary financial services.

As a free, open-source, non-profit, and non-custodial software project, the source code, documentation, technical communication, and related development activities of EPusdt should not be interpreted as authorization, endorsement, control, participation, warranty, or commitment by Good Morning Technology, LLC or its contributors regarding any user's downstream use, modification, deployment, integration, distribution, or secondary development.

Users independently determine how they use this project, for what purpose, and in what context. Good Morning Technology, LLC and its contributors cannot control, review, or restrict such conduct. Users are solely responsible for ensuring that their use, modification, deployment, integration, distribution, or derivative development complies with applicable laws, regulations, sanctions rules, and third-party rights in the relevant jurisdictions, and they bear all resulting risks, liabilities, and consequences.

Crypto assets are a high-risk emerging asset class. Stablecoins and other digital assets may experience sharp volatility, de-pegging, insufficient liquidity, technical failures, regulatory changes, or total loss of value. All code, documentation, and related materials in this project are provided on an "as is" and "as available" basis. Except where mandatory law provides otherwise, Good Morning Technology, LLC and its contributors are not liable for any direct or indirect loss resulting from use, inability to use, misuse, unlawful use, modification, deployment, integration, distribution, derivative development, or reliance on this project.

</details>
