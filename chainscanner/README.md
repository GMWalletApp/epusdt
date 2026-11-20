# ChainScanner

ChainScanner follows confirmed transaction activity once per configured network and delivers it to every subscriber watching a matching address. Payments and other consumers interpret the original transaction evidence independently.

## Run

With Go 1.25 or later, run `go build ./cmd/chainscanner`, then `./chainscanner -config chainscanner.json`. One scanner process owns the database exclusively. Other services connect to its HTTP API. Keep the SQLite database and its WAL on persistent storage. Bind to loopback or put the API behind TLS. Set each consumer's token to a separate randomly generated secret, for example the output of `openssl rand -hex 32`.

```json
{
  "listen": "127.0.0.1:9081",
  "database": "chainscanner.sqlite",
  "networks": [
    {"name":"ethereum","url":"http://127.0.0.1:8545","confirmations":12,"trace_calls":true},
    {"name":"aptos","url":"https://api.mainnet.aptoslabs.com","interval_seconds":2},
    {"name":"ton","url":"https://ton-blockchain.github.io/global.config.json"}
  ],
  "consumers": {"payments":"REPLACE_WITH_A_RANDOM_SECRET"}
}
```

The scanner starts at the latest eligible height. Set `lookback` before the first start to read a historical window. Each subsequent start resumes from the persisted height. A confirmation count of 1 includes the current head. Set the confirmation depth to the finality policy of your network. A deep chain reorganization after delivery requires external reconciliation. Reads retry from the last committed height on provider errors. Provision RPC throughput for whole blocks even when nobody is watching.

## Subscribe and consume

Send `Authorization: Bearer <token>` with each request. Consumers have independent subscriptions and queues.

| Method | Path | Result |
| --- | --- | --- |
| PUT | /v1/subscriptions | Replace all addresses for this consumer with a JSON array, e.g. `[{"network":"ethereum","address":"0x1111111111111111111111111111111111111111"}]`. Send `[]` to unsubscribe. Success returns 204. |
| GET | /v1/events/next | 200 and a delivery with `id` and `activity`, or 204 when caught up. |
| GET | /v1/heights/{network} | Last durably processed height, or -1 before the first block. |
| POST | /v1/events/{id}/ack | Acknowledge a delivery after recording the result. Returns 204. |

Registration takes effect at the next block commit. Each matching transaction is queued once per consumer with all its matching addresses. The block cursor and deliveries commit together. Unacknowledged events survive restart and unsubscription. Poll with one active consumer worker per token and deduplicate business effects by transaction or transfer identity. Queue growth follows the slowest consumers' outstanding activity.

## Network coverage

| Network ID | Evidence and address matching |
| --- | --- |
| ethereum, binance (BSC), polygon, plasma | Block transactions, sender, recipient, created contracts, log emitters and address-shaped indexed topics. Enable `trace_calls` and provide a node supporting the call tracer to include internal calls without logs. |
| tron | HTTP wallet API, contract parameters, transaction receipts, indexed logs and recorded internal transactions. |
| solana | Finalized produced slots, transaction account keys and pre/post SPL owners, including failed transactions. |
| ton | Masterchain and shard transactions, account plus internal message endpoints, original transaction BOC and master block identity. |
| aptos | Ordered ledger versions, senders, referenced Move addresses, events and write sets. |

TON and Aptos keep advancing with an empty watch list. The feed describes observable transaction participants and indexed address references. Arbitrary bytes embedded in contract storage, off-chain activity and nontransactional protocol rewards are outside the feed. An address appearing in an event does not by itself prove receipt of a payment.

Run `go test -race ./...` to exercise persistent delivery, consumer isolation and cursor recovery.

## Added mainnets and connection types

| Network name | Ledger | Recommended connection |
| --- | --- | --- |
| sui | Native Sui checkpoints | Sui gRPC v2 over HTTP/2, e.g. `https://fullnode.mainnet.sui.io:443` |
| arbitrum | Arbitrum One, 42161 | WSS from a full RPC provider or `https://arb1.arbitrum.io/rpc` |
| hyperevm | HyperEVM, 999 | `https://rpc.hyperliquid.xyz/evm` or a provider with full WSS RPC |
| monad | Monad, 143 | `wss://rpc.monad.xyz` |
| arc | Arc mainnet, 5042 | `wss://rpc.mainnet.arc.io` |
| avalanche | Avalanche C-Chain, 43114 | `https://api.avax.network/ext/bc/C/rpc` or full RPC over WSS |
| optimism | OP Mainnet, 10 | Full RPC over WSS or `https://mainnet.optimism.io` |

The EVM client checks `eth_chainId` on connection. A WSS node must support ordinary read calls as well as `eth_subscribe`. New heads wake the reader immediately. Notifications can be coalesced because the persisted cursor identifies the entire outstanding interval. HTTP nodes reuse connections and poll. Sui uses checkpoint stream notifications and reads numbered checkpoints from the same persistent gRPC channel. Every source also checks the tip on a timer; an unavailable subscription cannot strand the cursor. Latest L2 confirmations count L2 blocks and are distinct from Ethereum settlement.

Sui events include successful and failed transactions, senders, sponsors, balance owners, changed objects and owners, plus event evidence. Aptos and Sui have independent Move ledgers. Wrapped assets on EVM chains are identified by their host network and contract. HyperCore trading activity, Avalanche X/P chains and Arbitrum Nova have separate protocols or chain identities and are outside these adapters.

## Atomic subscription receipts

`PUT /v2/subscriptions` accepts the same authenticated full address list as v1. The response has one row per distinct address, in input order.

```json
[{"network":"arbitrum","address":"0x1111111111111111111111111111111111111111","from_height":525879818}]
```

`from_height` is inclusive. For a newly registered address it is the scanner's last committed height plus one, even if the provider is ahead or a block is being read. Unchanged addresses retain their original boundary across retries and restarts. Unsubscribe then re-register to start a new interval. A 409 means a requested network has no initialized cursor; the previous complete watch list stays in force. Retry after the node is ready. Existing v1 subscriptions from before this upgrade receive a conservative boundary on their first receipt; previously queued deliveries remain available. Aptos heights are ledger versions, Solana heights are slots, and Sui heights are checkpoint numbers.

Use `GET /v2/events/next?network=arbitrum&wait=15` for independent per-network consumption and acknowledge each delivery on the existing v1 endpoint. Waits of 1–20 seconds park outside the database and wake on commit. Run one consumer worker per network and token, or use the global v1 queue. EVM catch-up concurrently reads up to four blocks and commits them in height order. The in-memory address index is refreshed when membership changes. Both cursor and outbox still use the same SQLite transaction. No additional broker or database is required.
