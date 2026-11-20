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
