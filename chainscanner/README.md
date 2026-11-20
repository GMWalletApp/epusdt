# ChainScanner

ChainScanner follows confirmed transaction activity once per configured network and delivers it to every subscriber watching a matching address. Payments and other consumers interpret the original transaction evidence independently.

## Project layout

| Path | Responsibility |
| --- | --- |
| `scanner.go` | Public Go API imported as `github.com/GMWalletApp/ChainScanner`. |
| `cmd/chainscanner/` | Standalone server entry point and configuration. |
| `internal/chains/` | Network definitions, RPC transports, height probes and chain-specific transaction decoding. |
| `internal/engine/` | Scan loop, durable SQLite cursors, subscriptions and delivery queues. |
| `internal/feed/` | Consumer HTTP API, request and response types, and Go client. |
| `internal/nodes/` | Built-in catalog, node registry, failover and admin HTTP API. |
| `internal/testutil/` | Shared RPC test fixtures. |

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

The first watch on a network starts its worker. The normal first height is the observed head plus one. Additional watches start one height beyond the work already reserved for that network. The last removal across all consumers cancels RPC reads and streams, clears that chain’s scan progress and outstanding gaps and puts its worker to sleep. Re-registration after an idle period starts at the new head. An explicit positive `lookback` opts into a bounded historical window on activation. Confirmations delay delivery until a block reaches the configured depth.

After a stateless restart, resend the complete watch set and reconcile any payments in the interruption window using consumer-owned records. In-memory unacknowledged deliveries are lost on process exit. For durable subscriptions, cursors and an outbox, set `database` to a file path and keep the database and WAL on persistent storage. Existing deployments that used the former implicit database path can set `"database":"chainscanner.sqlite"` before upgrading to retain queued deliveries. Only one process can own that file.

## Subscribe and consume

Send `Authorization: Bearer <token>` with each request. Consumers have independent subscriptions and queues.

| Method | Path | Result |
| --- | --- | --- |
| PUT | /v1/subscriptions | Replace all addresses for this consumer with a JSON array, e.g. `[{"network":"ethereum","address":"0x1111111111111111111111111111111111111111"}]`. Send `[]` to unsubscribe. Success returns 204. |
| GET | /v1/events/next | 200 and a delivery with `id` and `activity`, or 204 when caught up. |
| GET | /v1/heights/{network} | Highest contiguous completed height, or -1 before initialization. A later block may already have delivered activity. |
| POST | /v1/events/{id}/ack | Acknowledge a delivery after recording the result. Returns 204. |

Each subscription has an inclusive start height. Each matching transaction is queued once per consumer with all its matching addresses. Completing a block and queuing its deliveries share one transaction. Events may arrive out of height order. Removing a watch preserves deliveries already queued in the current instance. Poll with one active consumer worker per token and deduplicate business effects by transaction or transfer identity. Queue growth follows the slowest consumers' outstanding activity.

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

The EVM client checks `eth_chainId` on connection. A WSS node must support ordinary read calls as well as `eth_subscribe`. New heads wake the reader immediately. Notifications can be coalesced because the scan state tracks both newly eligible heights and unfinished work. HTTP nodes reuse connections and poll. Sui uses checkpoint stream notifications and reads numbered checkpoints from the same persistent gRPC channel. Every active source also checks the tip on a timer; an unavailable subscription cannot strand the cursor. Latest L2 confirmations count L2 blocks and are distinct from Ethereum settlement.

Sui events include successful and failed transactions, senders, sponsors, balance owners, changed objects and owners, plus event evidence. Aptos and Sui have independent Move ledgers. Wrapped assets on EVM chains are identified by their host network and contract. HyperCore trading activity, Avalanche X/P chains and Arbitrum Nova have separate protocols or chain identities and are outside these adapters.

## Atomic subscription receipts

`PUT /v2/subscriptions` accepts the same authenticated full address list as v1. The response has one row per distinct address, in input order.

```json
[{"network":"arbitrum","address":"0x1111111111111111111111111111111111111111","from_height":525879818}]
```

`from_height` is inclusive. For a newly registered address it is one past the highest height already assigned to the scanner. Older outstanding gaps belong to the watches that were eligible at those heights. Unchanged addresses retain their boundary within the current session and across restarts in file-backed mode. Unsubscribe then re-register to start a new interval. A cold v2 registration queries the configured node before returning its inclusive start height. A 409 means that a boundary could not be established; the previous subscription set stays in force. V1 can register interest before a worker is ready. Only configured networks have workers. Retry after the node is ready. Existing v1 subscriptions from before this upgrade receive a conservative boundary on their first receipt; previously queued deliveries remain available. Aptos heights are ledger versions, Solana heights are slots, and Sui heights are checkpoint numbers.

Use `GET /v2/events/next?network=arbitrum&wait=15` for independent per-network consumption and acknowledge each delivery on the existing v1 endpoint. Waits of 1–20 seconds park outside the database and wake on commit. Run one consumer worker per network and token, or use the global v1 queue. The in-memory address index is refreshed when membership changes. The gap update and outbox inserts use the same SQLite transaction. No additional broker or database is required.

## Multi-node scheduling

Register each endpoint under its concrete network. The standalone registry and embedded payment worker share a round-robin scheduler. Up to eight endpoints fetch disjoint ranges concurrently using any supported mix of protocols. Transports paired to the same history URL take turns using it. Each endpoint keeps its connection while healthy. An empty watch list stops that network's worker.

For example, with an active watch starting at 101, nodes can read 101, 102 and 103 concurrently. If 101 is missing, activities from 102 and 103 are available to each matching subscriber as soon as those blocks finish. The scanner records the highest assigned height and the remaining gaps. In file-backed mode, restarting retries 101, accepts new blocks from 104 onward and retains unacknowledged deliveries with their original IDs. A subscriber added at 104 receives events from that height onward. The existing heights API reports 100 until the gap is filled.

Each scan round reserves up to four work items per eligible history endpoint, with eight active endpoints and 32 items at most. When the oldest request takes ten seconds, later work has completed and another eligible backend is idle, the scheduler cancels the request and reassigns it once. Completed heights are delivered independently of the repair. Each round shares capacity between eligible gaps and fresh work. Block-based networks schedule individual heights. Aptos batches up to 100 ledger versions, commits partial pages and splits failed pages to isolate missing versions. Reads respect the configured confirmation depth. Linked EVM reads check the hashes on either side of an outstanding gap, including across a restart with file-backed storage. A conflicting response leaves the gap open for retry. The public contiguous-range Go APIs keep their ordered return contract and the 32 MiB lookahead payload budget. The delivery path releases decoded activity once it has been queued.

Height checks and range reads track their own consecutive failures. Each group of three failures pauses the endpoint for 1, 3, 6, 10, 15 minutes and so on (triangular numbers, capped at one hour). After each cooldown the next eligible requests try the endpoint again. A successful range read resets both streaks. A successful height check resets the height streak only. Requests stop when all endpoints are cooling down and resume on a subsequent scan tick. Provider account quotas may be shared across different URLs.

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

The [built-in catalog](internal/nodes/catalog.go) includes Ethereum, Base, BNB Chain (`binance`), Polygon, Arbitrum One, Optimism, Arc, Monad, Avalanche C-Chain, HyperEVM, Plasma, Morph, Robinhood Chain, TON, TRON and the UniSat Bitcoin indexer. The five TRON IP endpoints use Wallet gRPC on TCP port 50051. UniSat reports the indexed Bitcoin mainnet height and appears with `scannable:false`.

To scan from the catalog, set a network entry such as `{"name":"base","confirmations":3}`. A configured URL, if present, takes priority at startup. A removed active node is replaced at the next scan step. All candidates share the network's original cursor, confirmation policy and subscriptions. Enabling a network requires a `networks` entry. Adding nodes by API makes them available to that network. Operator-supplied header credentials and history credentials stay with their endpoint. Supply project-specific Infura, GetBlock and MonadInfra URLs through private runtime configuration.

### ChainList discovery

The standalone server downloads the [ChainList RPC catalog](https://chainlist.org/rpcs.json) at startup and every 15 minutes. It matches the chain IDs of all supported EVM mainnets and probes public HTTPS and WSS URLs with up to eight concurrent requests to nodes. Discovering endpoints also runs when there are no subscriptions. Scan workers still start on demand.

To qualify, a candidate must report the expected chain ID, a positive block height and the corresponding block header with a timestamp within the last five minutes (allowing one minute of clock skew ahead). ChainList calculates its displayed score in the browser. Discovery accepts every candidate that passes the local check, including slower providers and those with any privacy label. A successful check measures head availability; archive retention, optional tracing and future rate limits vary by provider.

Use `GET /v1/admin/nodes?network=base` to see imported endpoints with `origin: "chainlist"`, height, probe time and latency. ChainList skips built-in, configured, custom and explicitly deleted identities. Identity includes the concrete network and interface. DNS hostname case, default ports, the root slash and EVM HTTP JSON-RPC aliases are normalized; paths and queries remain significant. Each successful refresh retires discovered nodes that disappeared or failed the check. An interrupted refresh or invalid catalog preserves the previous inventory. The scan pool drops a retired node and its cooldown on the next inventory reload. A rediscovered endpoint starts fresh. Built-ins, configured nodes and custom entries retain their own lifecycle. Deleting an imported endpoint suppresses rediscovery for the lifetime of the registry database.
