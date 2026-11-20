# ChainScanner

ChainScanner follows confirmed transaction activity once per configured network and delivers it to every subscriber watching a matching address. Payments and other consumers interpret the original transaction evidence independently.

## Run

With Go 1.25 or later, run `go build ./cmd/chainscanner`, then `./chainscanner -config chainscanner.json`. Standalone mode keeps subscriptions, active cursors and pending deliveries in memory by default. Consumers connect to its HTTP API. Bind to loopback or put the API behind TLS. Set each consumer's token to a separate randomly generated secret, for example the output of `openssl rand -hex 32`.

```json
{
  "listen": "127.0.0.1:9081",
  "networks": [
    {"name":"ethereum","url":"http://127.0.0.1:8545","confirmations":12,"trace_calls":true},
    {"name":"aptos","url":"https://api.mainnet.aptoslabs.com","interval_seconds":2},
    {"name":"ton","url":"https://ton-blockchain.github.io/global.config.json"}
  ],
  "consumers": {"payments":"REPLACE_WITH_A_RANDOM_SECRET"}
}
```

Each node requires a concrete network name. For example, use `ethereum` for chain ID 1, `base` for 8453 and `binance` for BSC (56). Registering an address on Base activates only the Base worker. A wrong execution chain ID fails validation before transaction reads.

The first watch on a network starts its worker. The normal first height is the observed head plus one. Additional watches start at the next uncommitted height. The last removal across all consumers cancels RPC reads and streams, forgets that chain’s cursor and puts its worker to sleep. Re-registration after an idle period starts at the new head. An explicit positive `lookback` opts into a bounded historical window on activation. Confirmations delay delivery until a block reaches the configured depth.

After a stateless restart, resend the complete watch set and reconcile any payments in the interruption window using consumer-owned records. In-memory unacknowledged deliveries are lost on process exit. For durable subscriptions, cursors and an outbox, set `database` to a file path and keep the database and WAL on persistent storage. Existing deployments that used the former implicit database path can set `"database":"chainscanner.sqlite"` before upgrading to retain queued deliveries. Only one process can own that file.

## Subscribe and consume

Send `Authorization: Bearer <token>` with each request. Consumers have independent subscriptions and queues.

| Method | Path | Result |
| --- | --- | --- |
| PUT | /v1/subscriptions | Replace all addresses for this consumer with a JSON array, e.g. `[{"network":"ethereum","address":"0x1111111111111111111111111111111111111111"}]`. Send `[]` to unsubscribe. Success returns 204. |
| GET | /v1/events/next | 200 and a delivery with `id` and `activity`, or 204 when caught up. |
| GET | /v1/heights/{network} | Last durably processed height, or -1 before the first block. |
| POST | /v1/events/{id}/ack | Acknowledge a delivery after recording the result. Returns 204. |

Registration takes effect at the next block commit. Each matching transaction is queued once per consumer with all its matching addresses. The block cursor and deliveries commit together. Removing a watch preserves deliveries already queued in the current instance. Poll with one active consumer worker per token and deduplicate business effects by transaction or transfer identity. Queue growth follows the slowest consumers' outstanding activity.

## Network coverage

| Network ID | Evidence and address matching |
| --- | --- |
| ethereum, binance (BSC), polygon, plasma | Block transactions, sender, recipient, created contracts, log emitters and address-shaped indexed topics. Enable `trace_calls` and provide a node supporting the call tracer to include internal calls without logs. |
| tron | HTTP wallet API, contract parameters, transaction receipts, indexed logs and recorded internal transactions. |
| solana | Finalized produced slots, transaction account keys and pre/post SPL owners, including failed transactions. |
| ton | Masterchain and shard transactions, account plus internal message endpoints, original transaction BOC and master block identity. |
| aptos | Ordered ledger versions, senders, referenced Move addresses, events and write sets. |

All networks, including TON and Aptos, remain idle with zero watched addresses. The feed describes observable transaction participants and indexed address references. Arbitrary bytes embedded in contract storage, off-chain activity and nontransactional protocol rewards are outside the feed. An address appearing in an event does not by itself prove receipt of a payment.

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
| base | Base Mainnet, 8453 | `wss://base-rpc.publicnode.com` or `https://mainnet.base.org` |
| optimism | OP Mainnet, 10 | Full RPC over WSS or `https://mainnet.optimism.io` |

The EVM client checks `eth_chainId` on connection. A WSS node must support ordinary read calls as well as `eth_subscribe`. New heads wake the reader immediately. Notifications can be coalesced because the persisted cursor identifies the entire outstanding interval. HTTP nodes reuse connections and poll. Sui uses checkpoint stream notifications and reads numbered checkpoints from the same persistent gRPC channel. Every active source also checks the tip on a timer; an unavailable subscription cannot strand the cursor. Latest L2 confirmations count L2 blocks and are distinct from Ethereum settlement.

Sui events include successful and failed transactions, senders, sponsors, balance owners, changed objects and owners, plus event evidence. Aptos and Sui have independent Move ledgers. Wrapped assets on EVM chains are identified by their host network and contract. HyperCore trading activity, Avalanche X/P chains and Arbitrum Nova have separate protocols or chain identities and are outside these adapters.

## Atomic subscription receipts

`PUT /v2/subscriptions` accepts the same authenticated full address list as v1. The response has one row per distinct address, in input order.

```json
[{"network":"arbitrum","address":"0x1111111111111111111111111111111111111111","from_height":525879818}]
```

`from_height` is inclusive. For a newly registered address it is the scanner's last committed height plus one, even if the provider is ahead or a block is being read. Unchanged addresses retain their boundary within the current session and across restarts in file-backed mode. Unsubscribe then re-register to start a new interval. A cold v2 registration queries the configured node before returning its inclusive start height. A 409 means that a boundary could not be established; the previous subscription set stays in force. V1 can register interest before a worker is ready. Only configured networks have workers. Retry after the node is ready. Existing v1 subscriptions from before this upgrade receive a conservative boundary on their first receipt; previously queued deliveries remain available. Aptos heights are ledger versions, Solana heights are slots, and Sui heights are checkpoint numbers.

Use `GET /v2/events/next?network=arbitrum&wait=15` for independent per-network consumption and acknowledge each delivery on the existing v1 endpoint. Waits of 1–20 seconds park outside the database and wake on commit. Run one consumer worker per network and token, or use the global v1 queue. EVM catch-up concurrently reads up to four blocks and commits them in height order. The in-memory address index is refreshed when membership changes. Both cursor and outbox still use the same SQLite transaction. No additional broker or database is required.

## Node protocols

Set `protocol` explicitly when several interfaces use HTTPS. Existing configurations keep their defaults. A node must expose the methods for the chosen interface. One network still has exactly one scanner, cursor and delivery queue per consumer.

| Network | Direct block or transaction reads | Additional interfaces |
| --- | --- | --- |
| Ethereum, BSC, Polygon, Plasma, Arbitrum One, Base, HyperEVM, Monad, Arc, Avalanche C-Chain, OP Mainnet | Execution JSON-RPC via HTTP(S) (`http` or `jsonrpc`) or WebSocket (`ws`) | Local Unix socket (`ipc`, e.g. `ipc:///run/geth.ipc`) and Geth-compatible `graphql` where enabled. Ethereum also accepts Beacon `sse` notifications paired with execution HTTP. |
| TRON | Wallet native HTTP (`rest` or `http`) or Wallet `grpc`. Solidified blocks are available via `solidity-http` and `solidity-grpc` (WalletSolidity). | `jsonrpc` uses Ethereum-compatible block identifiers and a Wallet HTTP history endpoint. The scanner checks the block hash before emitting native contracts, receipts and recorded internal transfers. |
| Solana | Finalized JSON-RPC via HTTP(S) (`http` or `jsonrpc`) | PubSub `ws` subscribes to roots. Yellowstone `grpc` subscribes to finalized slots. Both use an HTTP RPC history endpoint for skipped slots and full blocks. |
| Sui | Native v2 `grpc` (also the legacy HTTPS default), or `graphql` | gRPC streams checkpoints. GraphQL paginates checkpoint transactions and nested events and supports transaction lookup by digest. |
| Aptos | Fullnode REST (`rest` or `http`) | RawData `grpc` streams ledger versions. Indexer `graphql` reads processor progress. Both use fullnode REST for complete transaction evidence. GraphQL progress is capped by the REST ledger version. |
| TON | LiteServer/ADNL (`lite`, URL points to a global config), TON Center v2 native HTTP (`rest`, base URL ending in `/api/v2`), or v2 `jsonrpc` (full URL ending in `/api/v2/jsonRPC`) | All three traverse the masterchain and shard ancestry. The HTTP adapters require `getBlock` and check the file and root hashes of the returned BOC. |

For a paired interface, set `history_url` and, if needed, `history_api_key`. Each endpoint receives only its own credentials. Use `grpcs://host:port` or `https://host:port` for TLS gRPC and `grpc://host:port` for plaintext. TRON sends the provider key in `tron-pro-api-key`, Yellowstone in `x-token`, and Aptos and Sui in the bearer authorization header. The Ethereum SSE URL is the provider's `/eth/v1/events` route. GraphQL uses the full GraphQL URL. Internal EVM call tracing requires execution JSON-RPC with the call tracer enabled. Provider coverage and archival retention vary.

```json
{"name":"solana","protocol":"ws","url":"wss://api.mainnet-beta.solana.com","history_url":"https://api.mainnet-beta.solana.com"}
```

Protocol references include [Ethereum execution RPC](https://geth.ethereum.org/docs/interacting-with-geth/rpc), [Geth GraphQL](https://geth.ethereum.org/docs/interacting-with-geth/rpc/graphql), [Beacon events](https://ethereum.github.io/beacon-APIs/#/Events/eventstream), [TRON Wallet](https://developers.tron.network/reference/full-node-api-overview), [Solana PubSub](https://solana.com/docs/rpc/websocket), [Yellowstone](https://github.com/rpcpool/yellowstone-grpc), [Sui data APIs](https://docs.sui.io/develop/accessing-data), [Aptos indexing](https://aptos.dev/build/indexer) and [TON Center block BOCs](https://docs.ton.org/api/v2/rpc/get-block).

## Node administration

Set `admin_token` in the standalone server's JSON configuration to a random secret of at least 24 bytes, distinct from every consumer token. Send it as a bearer token to the following endpoints. The admin routes are disabled when the token is omitted. Anyone holding this token can choose RPC destinations. Protect it and the scanner database.

| Method | Path | Result |
| --- | --- | --- |
| GET | `/v1/admin/nodes` | Inventory with stable IDs, network, protocol, URL, origin and last test result. Add `?network=base` to filter. |
| POST | `/v1/admin/nodes/test` | Refresh all heights or one network with the same filter. Returns individual results, including failures. |
| POST | `/v1/admin/nodes/{id}/test` | Fetch this node's current head and persist the result. |
| POST | `/v1/admin/nodes` | Add a node with `{"network":"base","protocol":"http","url":"https://your-base-node.example"}`. Returns 201, or 409 for an existing entry. Optional fields are `api_key`, `history_url` and `history_api_key`. Paired protocols require a history URL. |
| DELETE | `/v1/admin/nodes/{id}` | Remove a node, including a built-in or configured node. Returns 204. File-backed mode retains deletion across restarts. Posting the endpoint again restores it. |

A test returns `height`, `height_unit`, `status`, `checked_at_ms` (Unix time) and `latency_ms`. The status is `untested`, `ok`, `timeout` or `unavailable`. Unsuccessful observations have a null height. The batch tests up to eight nodes at once, with one batch active per process. GET reads the stored results without spending RPC quota. The consumer heights endpoint reports the current scan cursor and returns -1 for idle chains. Explicit admin tests can query an idle node without starting its scanner. In memory mode, recreate custom node entries from configuration or your administration client after restart. For Beacon SSE the primary observation is a beacon slot. For Aptos it is a ledger version, for Sui a checkpoint, and for Solana a slot.

The [built-in catalog](nodes_builtin.go) includes Ethereum, Base, BNB Chain (`binance`), Polygon, Arbitrum One, Optimism, Arc, Monad, Avalanche C-Chain, HyperEVM, Plasma, Morph, Robinhood Chain, TON, TRON and the UniSat Bitcoin indexer. The five TRON IP endpoints use Wallet gRPC on TCP port 50051. UniSat reports the indexed Bitcoin mainnet height and appears with `scannable:false`.

To scan from the catalog, set a network entry such as `{"name":"base","confirmations":3}`. A configured URL, if present, takes priority at startup. A failed node rotates to the next candidate on retry. A removed active node is replaced at the next scan step. All candidates share the network's original cursor, confirmation policy and subscriptions. Enabling a network requires a `networks` entry. Adding nodes by API makes them available to that network. Operator-supplied header credentials and history credentials stay with their endpoint. Supply project-specific Infura, GetBlock and MonadInfra URLs through private runtime configuration.
