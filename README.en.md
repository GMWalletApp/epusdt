# Epusdt — Easy Payment USDT

<p align="center">
  <img src="https://gmwallet.app/favicon.png" alt="Epusdt Logo - Multi-chain Crypto Payment Gateway" width="120">
</p>

<p align="center">
  <strong>Open-source multi-chain crypto payment gateway · Top real-world adoption</strong>
</p>

<p align="center">
  <a href="./README.en.md">English</a> |
  <a href="./README.md">简体中文</a>
</p>

<p align="center">
  <a href="https://epusdt.com"><img src="https://img.shields.io/badge/Docs-epusdt.com-blue?style=for-the-badge" alt="Official Docs"></a>
  <a href="https://t.me/epusdt"><img src="https://img.shields.io/badge/Telegram-Channel-26A5E4?style=for-the-badge&logo=telegram&logoColor=white" alt="Telegram Channel"></a>
  <a href="https://t.me/epusdt_group"><img src="https://img.shields.io/badge/Telegram-Group-26A5E4?style=for-the-badge&logo=telegram&logoColor=white" alt="Telegram Group"></a>
</p>

<p align="center">
  <a href="https://github.com/GMWalletApp/epusdt/stargazers"><img src="https://img.shields.io/github/stars/GMWalletApp/epusdt?style=flat-square&color=f5c542" alt="GitHub Stars 3000+"></a>
  <a href="https://www.gnu.org/licenses/gpl-3.0.html"><img src="https://img.shields.io/badge/License-GPLv3-blue?style=flat-square" alt="GPLv3 License"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.16+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.16+"></a>
  <a href="https://github.com/GMWalletApp/epusdt/releases"><img src="https://img.shields.io/github/v/release/GMWalletApp/epusdt?style=flat-square&color=green" alt="Latest Release"></a>
</p>

---

## What is Epusdt?

**Epusdt** (Easy Payment USDT) is a self-hosted **multi-chain, multi-token crypto payment gateway** built with Go. It started as a TRC20-only solution and evolved into a broader **multi-chain receiving platform**, allowing websites and applications to accept crypto payments across multiple networks and token types. No third-party custody, no platform fees — funds go directly into your wallet.

> **GitHub Star 3000+** · **10+ supported platform integrations** · **Top real-world adoption among crypto payment tools**

Deploy it privately, integrate with the HTTP API, and start receiving crypto payments in minutes.

### Supported Networks and Tokens

| Network | Tokens |
|---------|--------|
| **TRC20** (Tron) | USDT, TRX |
| **ERC20** (Ethereum) | USDT, USDC, ETH |
| **Solana** | USDT, USDC |
| **BEP20** (BSC) | USDT, USDC, BNB |
| **Polygon** | USDT, USDC |
| **Aptos** | USDC, USDT |
| **More** | Expanding continuously |

> Actual support depends on the [latest release](https://github.com/GMWalletApp/epusdt/releases) and the [official docs](https://epusdt.com).

---

## Broad Compatibility, Plug and Play

No matter what kind of platform you operate, Epusdt can fit into existing payment integration patterns with **minimal or no business logic refactor**, giving you a fast path to crypto payments for global use cases:

| Category | Supported Systems |
|----------|-------------------|
| **AI Distribution** | [OneAPI](https://github.com/songquanpeng/one-api), [NewAPI](https://github.com/QuantumNous/new-api) |
| **Card/Voucher Platforms** | [Dujiaoka](https://dujiao-next.com/), [ACG Faka](https://github.com/lizhipay/acg-faka) |
| **Proxy Panels** | [V2Board](https://github.com/v2board/v2board), [XBoard](https://github.com/cedar2025/Xboard), [xiaoV2board](https://github.com/wyx2685/v2board/), [SSPanel](https://github.com/anankke/sspanel-uim) |
| **Website Ecosystem** | [WordPress](https://wordpress.com/), [WHMCS](https://www.whmcs.com/) |
| **Epay-Compatible Flow** | Works with platforms already using Epay-style payment interfaces |
| **More** | Simple HTTP API, often integrated within 10 minutes |

---

## Core Features

- **Multi-chain and multi-token** — TRC20, ERC20, BEP20, Polygon, Aptos, and more
- **Self-hosted** — full control of funds and wallet infrastructure
- **Low-dependency runtime** — a single binary can run the service; low-traffic setups do not require MySQL + Redis
- **Cross-platform** — supports x86 / ARM on Windows / Linux / Mac
- **Multi-wallet rotation** — rotates receiving addresses to improve concurrency handling
- **Async callback queue** — handles merchant notifications efficiently
- **HTTP API** — easy integration from any language or framework
- **Telegram Bot** — instant payment notifications and lightweight operations

---

## Documentation and Guides

Full documentation: **[epusdt.com](https://epusdt.com)**

Quick-start links:

| Guide | Description |
|-------|-------------|
| [Repo-local: epctl installer](wiki/EPCTL.en.md) | Linux binary install, upgrade, status inspection, and Docker validation |
| [Docker Deployment](https://epusdt.com/guide/installation/docker) | Recommended one-command setup |
| [aaPanel Deployment](https://epusdt.com/guide/installation/aapanel) | Great for aaPanel users |
| [Manual Deployment](https://epusdt.com/guide/installation/manual.html) | Full manual control |
| [Developer API Docs](https://epusdt.com) | Integration reference |

The repository also ships these top-level scripts:

- [`./epctl`](./epctl) for Linux binary install, upgrade, config inspection, service status, logs, and initial password retrieval
- [`./epctl-docker-test.sh`](./epctl-docker-test.sh) for real Ubuntu + systemd installation validation inside local Docker

---

## Project Structure

```text
Epusdt
├── epctl       Linux binary installation and operations script
├── src/        Core project source code
└── wiki/       Documentation assets and knowledge base
```

---

## How It Works

Epusdt listens to supported blockchain APIs or RPC nodes across multiple networks such as TRC20, ERC20, BEP20, and Polygon. It captures incoming token transfers in real time and matches ownership using **amount differentiation** and **time-bounded locks**:

```text
Workflow:
1. A customer initiates payment and needs to send 20.05 USDT
2. The system searches for an available wallet-address + amount combination
3. If address_1:20.05 is free -> lock it for 10 minutes and return it
4. If already occupied -> add 0.0001 and try the next amount combination
5. Background listeners keep watching wallet deposits and mark the order paid once the amount matches
```

![Epusdt Payment Flow](wiki/img/implementation_principle.jpg)

---

## Community and Support

If you hit an issue, please open a [GitHub Issue](https://github.com/GMWalletApp/epusdt/issues) first. That is the best way to get attention and help.

| Channel | Link |
|---------|------|
| Telegram Channel | [https://t.me/epusdt](https://t.me/epusdt) |
| Telegram Group | [https://t.me/epusdt_group](https://t.me/epusdt_group) |
| Official Docs | [https://epusdt.com](https://epusdt.com) |

---

## Star History

<a href="https://www.star-history.com/?type=date&repos=gmwalletapp%2Fepusdt">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=gmwalletapp/epusdt&type=date&legend=top-left" />
 </picture>
</a>

---

## License

Epusdt is released under the [GPLv3](https://www.gnu.org/licenses/gpl-3.0.html) license.

---

## Disclaimer and Terms of Use

EPusdt is developed and published by Good Morning Technology, LLC as free, open-source, non-profit, and non-custodial software for learning, research, and technical communication. Nothing in this project constitutes investment, financial, legal, tax, compliance, or any other professional advice, nor should it be interpreted as a guarantee regarding any asset, transaction outcome, revenue, fund safety, technical availability, or fitness for a particular purpose.

Good Morning Technology, LLC is an entity organized under United States law and will comply with applicable legal and regulatory obligations as required. Under U.S. regulatory frameworks, whether virtual currencies, digital assets, or related technical services constitute money transmission, a money services business (MSB), or another regulated activity generally depends on the specific business model, including whether the operator receives, holds, controls, or transmits funds or value, provides custody on behalf of users, or engages in exchange, payment, clearing, settlement, or other intermediary financial services.

As a free, open-source, non-profit, and non-custodial software project, the source code, documentation, technical communication, and related development activities of EPusdt should not be interpreted as authorization, endorsement, control, participation, warranty, or commitment by Good Morning Technology, LLC or its contributors regarding any user's downstream use, modification, deployment, integration, distribution, or secondary development.

Users independently determine how they use this project, for what purpose, and in what context. Good Morning Technology, LLC and its contributors cannot control, review, or restrict such conduct. Users are solely responsible for ensuring that their use, modification, deployment, integration, distribution, or derivative development complies with applicable laws, regulations, sanctions rules, and third-party rights in the relevant jurisdictions, and they bear all resulting risks, liabilities, and consequences.

Crypto assets are a high-risk emerging asset class. Stablecoins and other digital assets may experience sharp volatility, de-pegging, insufficient liquidity, technical failures, regulatory changes, or total loss of value. All code, documentation, and related materials in this project are provided on an "as is" and "as available" basis. Except where mandatory law provides otherwise, Good Morning Technology, LLC and its contributors are not liable for any direct or indirect loss resulting from use, inability to use, misuse, unlawful use, modification, deployment, integration, distribution, derivative development, or reliance on this project.

---

<p align="center">
  <sub>
    <b>Keywords:</b> USDT Payment Gateway · Crypto Payment · Multi-chain Payment · TRC20 Payment · ERC20 Payment · BEP20 Payment ·
    Self-hosted Crypto Gateway · OneAPI Payment · NewAPI Payment · Dujiaoka Payment · ACG Faka Payment ·
    V2Board Payment · XBoard Payment · SSPanel Payment Interface ·
    WordPress Crypto Payment · WHMCS USDT Payment · Polygon USDT ·
    Epusdt · Easy Payment USDT · Open Source Payment Gateway · Multi-chain Collection
  </sub>
</p>
