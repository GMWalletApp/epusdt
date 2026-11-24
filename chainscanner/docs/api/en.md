# ChainScanner · API reference

**English** · [简体中文](zh-CN.md) · [Tiếng Việt](vi.md) · [Español](es.md) · [Русский](ru.md)

[Home](../../README.md) · [Quick start](../guide/en.md#quick-start) · [Connection and authentication](#auth)  ·  [Replace subscriptions](#subscriptions)  ·  [Read and acknowledge deliveries](#deliveries)  ·  [Live subscriptions](#streaming)  ·  [Node administration](#nodes)  ·  [Handling errors](#errors)

Integrate one consumer from registration through acknowledgement, or manage the upstream node pool with a separate administrator token. All examples below use the local server from the quick start.

<a id="auth"></a>

## Connection and authentication

Use `Authorization: Bearer <token>` on every request. The token selects the consumer and its queue. All tokens must be distinct and at least 24 bytes. Admin routes are enabled by `admin_token`. Send JSON bodies with `Content-Type: application/json`. Successful responses with a body use JSON; HTTP 204 has an empty body. Handler errors normally return plain text.

```sh
BASE=http://127.0.0.1:9081
TOKEN=$(jq -r '.consumers.payments' chainscanner.json)
ADMIN=$(jq -r '.admin_token' chainscanner.json)
consumer() { curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
admin() { curl -sS -i -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' "$@"; }
```

<a id="routes"></a>

## Endpoints

| Role | HTTP | Success |
| --- | --- | --- |
| Consumer | [`PUT /v1/subscriptions`](#replace-watches) | `200 / 202` |
| Consumer | [`GET /v1/events/next`](#next) | `200 / 204` |
| Consumer | [`GET /v1/events/stream`](#streaming) · WebSocket | `101` |
| Consumer | [`POST /v1/events/{id}/ack`](#ack) | `204` |
| Consumer | [`GET /v1/heights/{network}`](#height) | `200` |
| Consumer | [`GET /v1/transactions/{network}/{hash}`](#transactions) | `200` |
| Administrator | [`GET /v1/admin/progress`](#scan-progress) | `200` |
| Administrator | [`GET /v1/admin/scan-retry-rate`](#scan-retry-rate) | `200`, `400` |
| Administrator | [`GET /v1/admin/nodes`](#list) | `200` |
| Administrator | [`POST /v1/admin/nodes`](#add) | `201` |
| Administrator | [`POST /v1/admin/nodes/fallback`](#fallback) | `201` |
| Administrator | [`POST /v1/admin/nodes/{id}/test`](#test-one) | `200` |
| Administrator | [`POST /v1/admin/nodes/test`](#test-all) | `200` |
| Administrator | [`DELETE /v1/admin/nodes/{id}`](#delete) | `204` |
| Administrator | [`GET /v1/admin/blacklist`](#blacklist) | `200` |
| Administrator | [`PUT /v1/admin/blacklist/{host}`](#blacklist) | `204` |
| Administrator | [`DELETE /v1/admin/blacklist/{host}`](#blacklist) | `204` |

<a id="transactions"></a>

## Look up a transaction

Send a consumer bearer token and the configured network ID. No address subscription is needed. A successful response contains `network`, the normalized `hash` and the chain-native `result`. EVM and TRON include the transaction and receipt.

For Bitcoin mainnet use `bitcoin` and the 64-character hexadecimal txid. Confirmed lookups return `txid`, the serialized transaction in `hex`, and `blockhash`; unconfirmed transactions are treated as `pending`. Scan deliveries carry the decoded transaction with spent prevouts in `activity.data.transaction` and identify its block in `activity.data.block_hash`.

Hash lookup prefers a fallback node and returns the first usable transaction. The scanner excludes the sources that supplied the matching block activity.

HTTP 404 with `X-ChainScanner-Evidence: not_found` means the node answered that the hash is absent. A payment service can suspend the order for review. Retry HTTP 502 and 503 with backoff while the order is confirming. These responses may report `pending` or `incomplete`.

A failed query rotates to other eligible nodes. With alternatives available, the scanner makes at least three retries within its request deadline. Set `?fallback_only=true` to limit the pool. For a previously delivered transaction, `?observed=true` requires a recorded scan source. A 503 keeps the payment pending if the source record or an independent endpoint is unavailable.

```sh
consumer "$BASE/v1/transactions/base/$TX_HASH"
consumer "$BASE/v1/transactions/base/$TX_HASH?fallback_only=true"
```

<a id="trust"></a>

### Deciding when to trust a transaction

Before crediting a payment, check execution success, confirmation depth, recipient, asset and amount against the order. The result reflects the selected RPC provider.

[Node blacklist](#blacklist)

<a id="subscriptions"></a>

## Replace subscriptions

Send the consumer’s **complete** watch list as a JSON array (up to 8 MiB). Each item requires `network` and `address` strings. Use the exact network identifier and an address valid for that ledger. Omitting a previous watch removes it. An empty array removes all watches while preserving already queued deliveries.

<a id="replace-watches"></a>

### `PUT /v1/subscriptions`

Each registration has an inclusive `from_height`. A new watch on a running chain starts after the highest assigned block. Removing a watch fixes its inclusive `to_height` at the observed head, or the highest already assigned block if higher. Invalid watches return **400**. If a required head is unavailable, **409** leaves the entire previous set in effect.

```sh
consumer -X PUT "$BASE/v1/subscriptions" --data '[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]'
```

```json
{"registrations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101}],"terminations":[]}
```

A **202** receipt means at least one removed address still has outstanding blocks. Repeat the same full list to check progress without changing the boundary. **200** means every termination in this receipt is complete. Completion includes confirmation, gap repair and queuing matching events. Consumers can still read and acknowledge them afterwards.

```json
{"registrations":[],"terminations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101,"to_height":110,"completed":false}]}
```

<a id="deliveries"></a>

## Read and acknowledge deliveries

<a id="next"></a>

### `GET /v1/events/next`

Required query `network` selects one supported chain. Optional integer `wait` enables long polling for 1–20 seconds. Omit it for an immediate response. Returns **200** with the next pending delivery or **204** when none is ready. Unknown or missing network, or an invalid wait, returns **400**.

```sh
consumer "$BASE/v1/events/next?network=base&wait=15"
```

The example shows the delivery envelope with abbreviated transaction evidence. A transaction matching multiple watched addresses produces one delivery per consumer. The address match can come from incoming, outgoing or failed transactions. Payment consumers must check direction, success, asset and amount before crediting.

```json
{
  "id": 17,
  "activity": {
    "confirmations": 3,
    "network": "base",
    "height": 12345679,
    "tx_id": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "time_ms": 1770000000000,
    "addresses": [
      "0x1111111111111111111111111111111111111111"
    ],
    "data": {
      "transaction": {
        "hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "to": "0x1111111111111111111111111111111111111111"
      }
    }
  }
}
```

| Field | Type | Behavior |
| --- | --- | --- |
| `id` | `int64` | Queue delivery ID, used in the ack path. |
| `activity.network, activity.height` | `string, int64` | Concrete network and ledger position. |
| `activity.tx_id` | `string` | On-chain transaction identifier. |
| `activity.confirmations, activity.time_ms` | `int64, int64` | Observed confirmation count and chain timestamp in Unix milliseconds. |
| `activity.addresses` | `string[]` | Subscribed addresses matched for this consumer. |
| `activity.data` | `JSON` | Chain-specific JSON evidence. EVM evidence can include `transaction`, `logs`, `receipt` and `trace`. |
| `activity.data.refresh_assets` | `string[]` | Token contracts to refresh after a recognized global balance event, omitted when empty. [EVM](../guide/en.md#asset-events) |

<a id="ack"></a>

### `POST /v1/events/{id}/ack`

Pass the positive delivery `id` in the path, with no request body. Acknowledgement is scoped to the authenticated consumer and returns **204**, including a retry after a successful ack. A nonpositive or malformed ID returns **400**. Fetching leaves the item pending. Persist effects before ack and deduplicate by network plus transaction or transfer identity so a crash between those steps is safe.

```sh
ID=17
consumer -X POST "$BASE/v1/events/$ID/ack"
```

<a id="height"></a>

### `GET /v1/heights/{network}`

Returns **200** with a JSON integer: the highest contiguous completed height, or `-1` when no cursor exists. The result can lag behind delivered events if an earlier block is missing. Aptos uses ledger versions, Solana slots and Sui checkpoints.

```sh
consumer "$BASE/v1/heights/base"
```

```json
12345677
```

<a id="streaming"></a>

## Live subscriptions

### From ChainScanner to your application

Open a WebSocket or bidirectional gRPC stream to receive matching activity as it enters the queue. Both share the existing consumer token, watches and acknowledgements with HTTP long polling. Run one reader per consumer and network across all transports. Different consumers can subscribe to the same address independently.

**Connect.** Use either command below from the repository root with the quick-start token. WebSocket uses HTTP/1.1 Upgrade and JSON text frames. gRPC uses HTTP/2 on the same port and the published protobuf schema. Send the commands interactively and keep the input stream open. For a remote deployment use WSS or gRPC over TLS with a reverse proxy that forwards WebSocket upgrades and HTTP/2 gRPC streams to this listener.

[activity.proto](../../api/activity/v1/activity.proto)

```sh
websocat -H="Authorization: Bearer $TOKEN" ws://127.0.0.1:9081/v1/events/stream
```

```sh
grpcurl -plaintext -import-path . -proto api/activity/v1/activity.proto -H "authorization: Bearer $TOKEN" -d @ 127.0.0.1:9081 chainscanner.activity.v1.ActivityFeed/Subscribe
```

**Open → ready.** Send one opening frame with a configured network. Include the complete desired watch set in `replace` only when changing the subscriptions for this consumer across **all networks**. An explicit empty set clears them all. Omit `replace` on reconnect to retain existing watches. The server returns `ready`. When replacing, the receipt lists each canonical address and inclusive start height. When resuming, the receipt is empty.

Stream replacements report the same bounds in `ready.terminations`. In ProtoJSON, `toHeight` is a decimal string and an omitted `completed` means false.

```json
{"open":{"network":"base","replace":{"watches":[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]}}}
```

```json
{
  "ready": {
    "registrations": [
      {
        "network": "base",
        "address": "0x1111111111111111111111111111111111111111",
        "fromHeight": "12345678"
      }
    ]
  }
}
```

**Delivery → commit → ack.** Each stream has one outstanding delivery. Decode `dataJson` as chain-specific JSON and record the business effect durably. Then send the matching `ack` on the same connection. The next event follows when available. Use the actual ID received. In ProtoJSON, 64-bit integers are decimal strings. Normal queue idleness produces no application frames.

```json
{
  "delivery": {
    "id": "17",
    "network": "base",
    "height": "12345679",
    "txId": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "timeMs": "1770000000000",
    "confirmations": "3",
    "addresses": [
      "0x1111111111111111111111111111111111111111"
    ],
    "dataJson": "{\"transaction\":{\"to\":\"0x1111111111111111111111111111111111111111\"}}"
  }
}
```

```json
{"ack":"17"}
```

**Disconnects and errors.** Reconnect with backoff and send `open` again. An unacknowledged delivery is eligible for redelivery, including over another transport. Deduplicate business effects by network and transaction or transfer identity. Invalid credentials produce HTTP 401 at the WebSocket handshake or gRPC `UNAUTHENTICATED`. A bad network or wrong ack closes WebSocket with 1008 or returns `INVALID_ARGUMENT`. A missing registration boundary uses 1013 or `FAILED_PRECONDITION`. Temporary store errors use 1013 or `UNAVAILABLE`. Configure the gRPC client's receive-size limit for the largest transaction evidence you expect.

```json
{"open":{"network":"base"}}
```

WebSocket clients must answer protocol Ping frames with Pong. Use a backend client that can set the Authorization header. The browser’s native WebSocket constructor cannot set that header. For gRPC clients, keep the send side open for acknowledgements and cancel the RPC to stop an idle stream. Embedded Go servers must enable HTTP/2 to serve gRPC. The standalone binary enables cleartext HTTP/2 on its listener.

### HTTP long polling

Applications receive matched transactions via HTTP long polling. After registering the full watch list, run one reader per consumer and network. Set the client and proxy timeouts above `wait`. The example below uses the quick-start token, Python 3 and a local SQLite inbox. It resumes polling immediately on 204 and acknowledges a delivery only after committing the evidence.

```sh
export BASE TOKEN
python3 - <<'PY'
import http.client
import json
import os
import sqlite3
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

base = os.environ["BASE"].rstrip("/")
headers = {"Authorization": "Bearer " + os.environ["TOKEN"]}

def request(path, method="GET"):
    req = Request(base + path, headers=headers, method=method)
    with urlopen(req, timeout=30) as res:
        if res.status != 204:
            return json.load(res)

db = sqlite3.connect("base-inbox.sqlite")
db.execute("""CREATE TABLE IF NOT EXISTS inbox (
    network TEXT, tx_id TEXT, activity TEXT NOT NULL, PRIMARY KEY (network, tx_id)
)""")
delay = 1
while True:
    try:
        delivery = request("/v1/events/next?network=base&wait=20")
        if delivery is not None:
            activity = delivery["activity"]
            with db:
                db.execute("INSERT INTO inbox VALUES (?, ?, ?) "
                           "ON CONFLICT(network, tx_id) DO NOTHING",
                           (activity["network"], activity["tx_id"], json.dumps(activity)))
            request("/v1/events/{}/ack".format(delivery["id"]), "POST")
        delay = 1
    except HTTPError as err:
        if err.code != 429 and err.code < 500:
            raise
    except (URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
        pass
    else:
        continue
    time.sleep(delay)
    delay = min(30, 2 * delay)
PY
```

One local row holds one transaction per network for this consumer. If processing individual transfers, include their chain-specific indices in your business keys. A lost acknowledgement can cause redelivery. Restarting a memory-only scanner clears subscriptions and pending deliveries. Re-register the complete list and reconcile the gap against your own checkpoint and chain history. File-backed SQLite preserves the queue and watches between restarts.

### From a node to ChainScanner

Choose a node interface for the specific network. A new head wakes the scanner. It still reads the underlying chain data and tracks completed heights across disconnects. The stream retries after five seconds while the source is active. With no watched addresses on a network, its scanner goes idle.

| Network | Notifications | Historical data |
| --- | --- | --- |
| EVM networks | WebSocket JSON-RPC · `eth_subscribe("newHeads")` | Execution RPC on the same node |
| Avalanche | Official C-Chain WebSocket · `newHeads` | The built-in HTTP companion verifies chain ID `43114` and serves block and transaction reads |
| Solana | WebSocket · `rootSubscribe`, or Yellowstone gRPC finalized slot stream | HTTP JSON-RPC in `history_url` |
| Sui | gRPC · `SubscribeCheckpoints` | Checkpoint reads on the same gRPC node |
| Aptos | RawData gRPC transaction stream | Fullnode REST in `history_url` |

```json
{
  "name": "solana",
  "protocol": "ws",
  "url": "wss://api.mainnet-beta.solana.com",
  "history_url": "https://api.mainnet-beta.solana.com"
}
```

**Ethereum Beacon (optional).** A Beacon node can send head notifications over an open HTTP response using Server-Sent Events (SSE). Set `protocol` to `sse` and `url` to the node’s `/eth/v1/events` endpoint. Set `history_url` to an Ethereum execution-layer HTTP RPC. The scanner requests the `head` topic and uses execution block heights for its cursor. An Ethereum execution node with WebSocket already supplies head notifications on its own.

<a id="nodes"></a>

## Node administration

<a id="list"></a>

### `GET /v1/admin/nodes`

Optional query `network` filters the inventory. Returns **200** with a JSON array of nodes and stored probe results. Listing spends no upstream RPC quota; an empty result is `[]`.

```sh
admin "$BASE/v1/admin/nodes?network=base"
```

```json
[
  {
    "id": 42,
    "network": "base",
    "protocol": "http",
    "url": "https://rpc.example.org",
    "origin": "custom",
    "status": "untested",
    "height": null,
    "checked_at_ms": null,
    "latency_ms": null,
    "height_unit": "block",
    "scannable": true
  }
]
```

| Field | Type | Behavior |
| --- | --- | --- |
| `id, network` | `int64, string` | Stable numeric ID and concrete network. |
| `protocol, url` | `string, string` | Selected transport and endpoint URL. |
| `origin` | `string` | One of `builtin`, `config`, `custom`, `chainlist`, `openchainbench`, `comparenodes`, `fallback`. |
| `status` | `string` | One of `untested`, `ok`, `timeout`, `unavailable`. Inspect this field after probing. |
| `height, height_unit` | `int64? , string` | Observed upstream height, or null. Unit is `block`, `slot`, `checkpoint`, `ledger_version` or `beacon_slot`. |
| `checked_at_ms, latency_ms` | `int64?, int64?` | Unix milliseconds of probe start and elapsed milliseconds. Both are null before the first test. |
| `read_failure, read_retry_at_ms` | `string?, int64?` | An active scanner reports a failed read as `status: unavailable`. The reason survives successful height probes until a read succeeds. The optional retry time is the cooldown deadline in Unix milliseconds. |
| `scannable` | `bool` | Whether the endpoint can scan transactions. Height-only endpoints never receive scan tasks. |

<a id="add"></a>

### `POST /v1/admin/nodes`

Send `network` and `url`; specify `protocol` explicitly to select the intended interface. Optional strings: `api_key`, `history_url`, `history_api_key`. Paired transports require a separate history URL. Unknown fields, invalid options or malformed JSON return **400**. The body limit is 8 KiB. Returns **201** with the node, or **409** for an existing entry. Adding a node does not enable a network worker by itself.

```sh
admin -X POST "$BASE/v1/admin/nodes" --data '{"network":"base","protocol":"http","url":"https://rpc.example.org"}'
```

<a id="fallback"></a>

### `POST /v1/admin/nodes/fallback`

Register an operator-funded history endpoint for one network with the same fields and administrator token as normal node creation. The response is **201** with `origin: fallback`. After a normal range read fails, a free fallback can take that range immediately. It also handles previously failed ranges recovered from SQLite. The scheduler checks the fallback’s own confirmation height for the repair. Fresh blocks, subscription boundaries and automatic head polling use ordinary nodes. Explicit administrator tests can contact the paid endpoint. Failed repairs remain in the retry queue and the same three-failure cooldown applies. List, test and delete by the returned node ID. Failed ranges rotate across eligible fallback nodes. Register each node separately.

```sh
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://second-rpc.example.net","api_key":"SECOND_PROVIDER_KEY"}'
```

<a id="scan-progress"></a>

### `GET /v1/admin/progress`

Returns one row per configured network. Reading this endpoint makes no RPC calls. `target_height` includes the configured confirmation depth. `cursor_height` is the last contiguous completed height. `lag_ms` is a lower bound on how long an observed eligible height has been waiting for that cursor. Samples are held in memory for the current watch period, at most 361 spaced at least five seconds apart. A restart starts a new observation window. Status is `warning` above 300,000 ms and `ok` otherwise. `idle`, `warming` and `stale` have null lag. A head becomes stale after five minutes or twice the poll interval, whichever is longer.

`scan_success_count` and `scan_failure_count` count scan ranges completed or failed in the current uninterrupted watch period. Each range has one outcome after provider failover. Retrying a failed range later adds a new outcome. Head polls, node health probes, idle time and canceled work contribute no samples. Both counts start at zero and reset after the final watch ends or the scanner restarts. Older scanners omit both fields.

<a id="scan-retry-rate"></a>

### `GET /v1/admin/scan-retry-rate`

Use the administrator token. Omit `network` to list configured networks, or supply one configured network. An empty, unknown or repeated network returns **400**.

`total_attempts` counts provider range reads that started after the node passed the height check. `retry_attempts` counts those that reread a range or repair a gap marked failed, including fallback reads. Each attempt counts once even for a multi-height range or several RPC calls. Head probes, transaction lookups and delivery retries are excluded. Counters accumulate in memory across watch periods until the scanner restarts. `retry_rate` is `retry_attempts / total_attempts` (0–1), or `null` before the first read. To measure an interval within one run, divide the change in retries by the change in attempts.

```sh
admin "$BASE/v1/admin/scan-retry-rate?network=base"
```

```json
[{"network":"base","total_attempts":100,"retry_attempts":4,"retry_rate":0.04}]
```

<a id="test-one"></a>

### `POST /v1/admin/nodes/{id}/test`

Path `id` must be a positive integer. No request body. Returns **200** with the refreshed node, including an unsuccessful probe recorded as `timeout` or `unavailable`. Invalid ID: **400**. Unknown or removed ID: **404**.

```sh
NODE_ID=$(curl -fsS -H "Authorization: Bearer $ADMIN" "$BASE/v1/admin/nodes?network=base" | jq -er '.[] | select(.url == "https://rpc.example.org" and .origin == "custom") | .id') &&
admin -X POST "$BASE/v1/admin/nodes/$NODE_ID/test"
```

<a id="test-all"></a>

### `POST /v1/admin/nodes/test`

Probe all visible nodes, optionally filtered by `network`. No request body. Returns **200** with a node array, including failed probes. Up to eight probes run in parallel. A concurrent batch request returns **409**. Client cancellation stops the batch.

```sh
admin -X POST "$BASE/v1/admin/nodes/test?network=base"
```

<a id="delete"></a>

### `DELETE /v1/admin/nodes/{id}`

Remove a node by positive `id`, including a built-in or configured node. Returns **204**, **400** for an invalid ID or **404** for an absent node. File-backed storage preserves removal across restarts. Posting the endpoint again can restore it.

```sh
admin -X DELETE "$BASE/v1/admin/nodes/$NODE_ID"
```

<a id="blacklist"></a>

### Node blacklist

Use an administrator token. A domain rule matches the domain and all its subdomains across ports and networks. An IP literal matches that address. Rules inspect both the registered endpoint URL and its history URL before selection or discovery probes. They do not resolve DNS aliases or identify hidden upstreams behind proxies or TON global configurations.

Use `keyword:acme` for a case-insensitive substring of the hostname, `exact:rpc.example.org` for a single host, or an unprefixed domain to include subdomains. The same spelling works in the configuration and both admin routes. Paths, credentials and query strings are excluded from keyword matching.

```sh
admin -X PUT "$BASE/v1/admin/blacklist/keyword:acme"
admin -X PUT "$BASE/v1/admin/blacklist/exact:rpc.example.org"
admin "$BASE/v1/admin/blacklist"
admin -X DELETE "$BASE/v1/admin/blacklist/keyword:acme"
admin -X DELETE "$BASE/v1/admin/blacklist/exact:rpc.example.org"
```

List returns a sorted JSON array of `{host, source}`. PUT and DELETE are idempotent and return 204. Rules take effect for new selections, and active scanners refresh their pools. File-backed SQLite preserves API rules across restarts. Set `"blacklist": ["rpc.example.org"]` at the top level of the server configuration to pin a rule. Deleting a configuration rule through the API returns 409. Edit the file and restart to remove it.

<a id="errors"></a>

## Handling errors

Correct invalid requests before retrying **400**. For **401**, verify the bearer token and its role. Inspect the endpoint-specific meaning of **409** above. A **503** indicates that the subscription, queue, cursor or node store could not serve the operation. Retry transient failures with backoff. An empty queue returns **204**.

[Guide](../../README.md) · [ConsumerHandler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/feed/handler.go) · [NodeRegistry.Handler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/nodes/http.go)

### Node management capabilities

`GET /v1/admin/capabilities` lists the available protocols and reports support for node settings and migration. Use `PATCH /v1/admin/nodes/{id}` with any of `enabled`, `role`, `purpose`, `weight`, `url`, `api_key`, `history_url` and `history_api_key`. Omitted settings retain their values. The role is either `regular` or `fallback`. Disabling a node preserves its settings across restarts and discovery. Set `purpose` to `manual_verify` to reserve a regular node for manual verification. An explicit manual lookup accepts `manual=true`. Batch health checks skip disabled nodes and accept `include_manual=true` to include manual-only nodes.
