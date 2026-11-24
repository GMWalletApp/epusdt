# ChainScanner · 接口参考

[English](en.md) · **简体中文** · [Tiếng Việt](vi.md) · [Español](es.md) · [Русский](ru.md)

[首页](../../README.zh-CN.md) · [快速开始](../guide/zh-CN.md#quick-start) · [连接与鉴权](#auth)  ·  [替换订阅](#subscriptions)  ·  [读取与确认投递](#deliveries)  ·  [长连接与订阅](#streaming)  ·  [节点管理](#nodes)  ·  [错误处理](#errors)

消费者使用自己的令牌注册订阅、读取并确认事件。节点管理使用独立的管理员令牌。以下示例沿用快速开始中的本机服务。

<a id="auth"></a>

## 连接与鉴权

每次请求都发送 `Authorization: Bearer <token>`，服务按令牌确定消费者及其队列。所有令牌必须互不相同，长度至少 24 字节。配置 `admin_token` 后才开启管理路由。JSON 请求体使用 `Content-Type: application/json`。成功且有正文时返回 JSON；HTTP 204 没有正文。处理器返回的错误通常是纯文本。

```sh
BASE=http://127.0.0.1:9081
TOKEN=$(jq -r '.consumers.payments' chainscanner.json)
ADMIN=$(jq -r '.admin_token' chainscanner.json)
consumer() { curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
admin() { curl -sS -i -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' "$@"; }
```

<a id="routes"></a>

## 接口索引

| 角色 | HTTP | 成功状态 |
| --- | --- | --- |
| 消费者 | [`PUT /v1/subscriptions`](#replace-watches) | `200 / 202` |
| 消费者 | [`GET /v1/events/next`](#next) | `200 / 204` |
| 消费者 | [`GET /v1/events/stream`](#streaming) · WebSocket | `101` |
| 消费者 | [`POST /v1/events/{id}/ack`](#ack) | `204` |
| 消费者 | [`GET /v1/heights/{network}`](#height) | `200` |
| 消费者 | [`GET /v1/transactions/{network}/{hash}`](#transactions) | `200` |
| 管理员 | [`GET /v1/admin/progress`](#scan-progress) | `200` |
| 管理员 | [`GET /v1/admin/scan-retry-rate`](#scan-retry-rate) | `200`, `400` |
| 管理员 | [`GET /v1/admin/nodes`](#list) | `200` |
| 管理员 | [`POST /v1/admin/nodes`](#add) | `201` |
| 管理员 | [`POST /v1/admin/nodes/fallback`](#fallback) | `201` |
| 管理员 | [`POST /v1/admin/nodes/{id}/test`](#test-one) | `200` |
| 管理员 | [`POST /v1/admin/nodes/test`](#test-all) | `200` |
| 管理员 | [`DELETE /v1/admin/nodes/{id}`](#delete) | `204` |
| 管理员 | [`GET /v1/admin/blacklist`](#blacklist) | `200` |
| 管理员 | [`PUT /v1/admin/blacklist/{host}`](#blacklist) | `204` |
| 管理员 | [`DELETE /v1/admin/blacklist/{host}`](#blacklist) | `204` |

<a id="transactions"></a>

## 按哈希查询交易

使用消费者令牌，指定已配置的网络，无需订阅地址。成功响应包含 `network`、规范化后的 `hash` 和链原生数据 `result`。EVM 和 TRON 返回交易及回执。

Bitcoin 主网键为 `bitcoin`，哈希使用 64 位十六进制 txid。已入块交易返回 `result.txid`、完整序列化交易 `result.hex` 及 `result.blockhash`，未入块交易视为 `pending`。扫描事件的 `activity.data.transaction` 包含按块取得的交易和前序输出，`activity.data.block_hash` 标明区块。

按哈希查询优先使用兜底节点，取得可用交易内容即返回。Scanner 排除曾经扫到这笔订阅交易的节点。

HTTP 404 且响应头为 `X-ChainScanner-Evidence: not_found` 表示节点明确返回查无该交易，支付系统可挂起订单。遇到 502 或 503 时保持确认中，延迟后继续查询。响应头也可能标记 `pending` 或 `incomplete`。

查询失败时轮换可用节点。在请求时限内有其他节点可用时，首次失败后至少重试三次。`?fallback_only=true` 限定只用兜底节点。`?observed=true` 要求 Scanner 有这笔订阅交易的扫块来源记录。缺少来源记录或没有可供复查的节点时返回 503，订单继续等待。

```sh
consumer "$BASE/v1/transactions/base/$TX_HASH"
consumer "$BASE/v1/transactions/base/$TX_HASH?fallback_only=true"
```

<a id="trust"></a>

### 什么时候需要二次确认

入账前应核对交易执行状态、确认深度、收款地址、币种和金额。查询结果来自本次选中的 RPC 节点。

[节点黑名单](#blacklist)

<a id="subscriptions"></a>

## 替换订阅

请求体为该消费者的**完整订阅列表**，JSON 数组上限 8 MiB。每项含字符串 `network` 和 `address`，分别为具体网络 ID 及该网络上的合法地址。旧订阅未出现在新列表中即被移除。发送空数组可全部退订，已排队事件仍可读取。

<a id="replace-watches"></a>

### `PUT /v1/subscriptions`

每个地址的 `from_height` 都含首块。在运行中的链上，新订阅从已分配最高块的下一块开始。移除地址时读取链头，并用它与已分配最高块中较高者作为 `to_height`，该块仍在监听范围内。地址或网络无效返回 **400**。无法取得所需链头时返回 **409**，原订阅整组保留。

```sh
consumer -X PUT "$BASE/v1/subscriptions" --data '[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]'
```

```json
{"registrations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101}],"terminations":[]}
```

返回 **202** 表示至少一个退订地址仍有区块待处理。重复提交同一份完整列表可查询进度，终止块保持固定。返回 **200** 表示本次回执中的退订均已完成，区块已达到确认数、缺块已补齐、匹配事件已入队。消费者之后仍可领取并确认这些事件。

```json
{"registrations":[],"terminations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101,"to_height":110,"completed":false}]}
```

<a id="deliveries"></a>

## 读取与确认投递

<a id="next"></a>

### `GET /v1/events/next`

必填查询参数 `network` 指定一条受支持的链。可选整数 `wait` 开启 1–20 秒长轮询；省略即立即返回。有待处理投递时返回 **200**，没有则返回 **204**。缺少或不支持的网络、非法等待时间均返回 **400**。

```sh
consumer "$BASE/v1/events/next?network=base&wait=15"
```

下例展示投递信封，交易证据已简化。同一笔交易同时匹配多个监听地址时，每个消费者只收到一份。转入、转出和失败交易都可能命中地址；支付端应核对方向、执行结果、资产与金额后再入账。

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

| 字段 | 类型 | 行为 |
| --- | --- | --- |
| `id` | `int64` | 队列投递 ID，用在确认接口路径中。 |
| `activity.network, activity.height` | `string, int64` | 具体网络与链上位置。 |
| `activity.tx_id` | `string` | 链上交易标识。 |
| `activity.confirmations, activity.time_ms` | `int64, int64` | 观察到的确认数、链上时间戳（Unix 毫秒）。 |
| `activity.addresses` | `string[]` | 该消费者本次匹配到的监听地址。 |
| `activity.data` | `JSON` | 链专属 JSON 证据。EVM 数据可包含 `transaction`、`logs`、`receipt`、`trace`。 |
| `activity.data.refresh_assets` | `string[]` | 识别到全局余额调整时，列出需要刷新的代币合约；为空时省略。 [EVM](../guide/zh-CN.md#asset-events) |

<a id="ack"></a>

### `POST /v1/events/{id}/ack`

路径填正整数投递 `id`，不传正文。确认仅作用于当前令牌的队列，成功返回 **204**；重复确认已处理的 ID 同样返回 **204**。无效或非正 ID 返回 **400**。读取本身不会移除事件。先落库再确认，并按网络与交易或转账标识去重，避免两步之间崩溃造成重复入账。

```sh
ID=17
consumer -X POST "$BASE/v1/events/$ID/ack"
```

<a id="height"></a>

### `GET /v1/heights/{network}`

返回 **200** 和一个 JSON 整数，表示已连续完成的最高高度。没有游标时为 `-1`。存在较早缺块时，已投递事件的高度可高于此值。Aptos 使用账本版本，Solana 使用 slot，Sui 使用检查点。

```sh
consumer "$BASE/v1/heights/base"
```

```json
12345677
```

<a id="streaming"></a>

## 长连接与订阅

### 业务服务如何持续接收事件

业务服务可以建立 WebSocket 或 gRPC 双向流，在匹配的地址活动进入队列时接收推送。两种方式与 HTTP 长轮询共用消费者令牌、订阅和确认状态。每个消费者在每条网络上选一种方式运行一个读取器；多个消费者可以独立订阅同一地址。

**建立连接。** 在仓库根目录，用快速开始里的令牌任选下面一条命令。WebSocket 使用 HTTP/1.1 Upgrade 和 JSON 文本帧，gRPC 通过同一端口使用 HTTP/2 和公开的 protobuf 定义。以交互方式输入消息，保持输入流开启。远程部署时使用 WSS 或 TLS gRPC，在反向代理上分别转发 WebSocket Upgrade 和 HTTP/2 gRPC 流。

[activity.proto](../../api/activity/v1/activity.proto)

```sh
websocat -H="Authorization: Bearer $TOKEN" ws://127.0.0.1:9081/v1/events/stream
```

```sh
grpcurl -plaintext -import-path . -proto api/activity/v1/activity.proto -H "authorization: Bearer $TOKEN" -d @ 127.0.0.1:9081 chainscanner.activity.v1.ActivityFeed/Subscribe
```

**打开流 → 收到回执。** 第一帧的 `network` 必须是已配置的具体网络。只有要替换该消费者在**所有链**的完整订阅时才带 `replace`，显式传空集合会清空全部订阅。重连时省略 `replace` 即可沿用现有订阅。服务端返回 `ready`，替换成功时包含规范化地址和起始高度（含首块）；沿用现有订阅时回执为空。

通过长连接替换订阅时，`ready.terminations` 返回相同的退订边界。ProtoJSON 中的 `toHeight` 是十进制字符串，省略 `completed` 表示尚未完成。

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

**接收 → 持久化 → 确认。** 每条流一次推送一笔待确认事件。按链解析 `dataJson` 中的原始 JSON，持久化业务结果，再在原连接上发送相同投递 ID 的 `ack`。确认成功后才会推送下一笔。下面的 ID 仅作示例。ProtoJSON 用十进制字符串表示 64 位整数。队列空闲时没有业务消息。

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

**断线与错误。** 退避重连后重新发送 `open`。未确认事件可能再次投递，也可以改用另一种协议接收，业务侧应按网络和交易或转账标识去重。令牌无效时 WebSocket 握手返回 HTTP 401，gRPC 返回 `UNAUTHENTICATED`。链名错误或确认 ID 不符时分别返回关闭码 1008 或 `INVALID_ARGUMENT`。起始高度暂不可用时返回 1013 或 `FAILED_PRECONDITION`；队列存储暂不可用时返回 1013 或 `UNAVAILABLE`。gRPC 客户端的最大接收消息大小应能容纳预期的最大交易证据。

```json
{"open":{"network":"base"}}
```

WebSocket 客户端需响应协议层的 Ping 帧并回送 Pong。请使用能设置 Authorization 请求头的后端客户端；浏览器原生 WebSocket 构造函数无法设置此头。gRPC 客户端应保持发送端开启以便确认，停止空闲订阅时取消 RPC。嵌入式 Go 服务要启用 HTTP/2 才能提供 gRPC；独立二进制已在监听端口启用明文 HTTP/2。

### HTTP 长轮询

业务方通过 HTTP 长轮询接收匹配交易。先注册完整地址列表，再为每个消费者的每条链启动一个读取循环。客户端及反向代理的超时应长于 `wait`。下例使用快速开始里的令牌、Python 3 和本地 SQLite 收件箱。收到 204 立即发起下一次请求，收到事件则先提交到本地数据库，再确认投递。

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

示例为此消费者按网络和交易保存一条记录。如果同一交易含有多笔需分别入账的转账，应将链上转账索引也纳入业务去重键。确认响应丢失时可能再次收到投递。纯内存扫描器重启会丢失订阅和待投递事件，业务方要重新注册完整列表，并依据自己的检查点和链上历史核对空档。文件模式的 SQLite 可在重启后保留订阅和队列。

### 链节点如何通知 ChainScanner

为具体网络选择节点接口。收到新区块头通知后，扫描器会读取链上数据，并根据连续完成的高度在断线后补扫。节点流处于启用状态时，断开后等待 5 秒重连。某条链没有监听地址时，该链的扫描器进入空闲。

| 网络 | 新区块或进度通知 | 历史数据来源 |
| --- | --- | --- |
| 各 EVM 网络 | WebSocket JSON-RPC · `eth_subscribe("newHeads")` | 同一节点的执行层 RPC |
| Avalanche | 官方 C-Chain WebSocket · `newHeads` | 配套的官方 HTTP 接口校验链 ID `43114`，读取区块和交易 |
| Solana | WebSocket · `rootSubscribe` 或 Yellowstone gRPC 的最终确认 slot 通知 | 在 `history_url` 配置 HTTP JSON-RPC |
| Sui | gRPC · `SubscribeCheckpoints` | 用同一 gRPC 节点读取检查点 |
| Aptos | RawData gRPC 交易流 | 在 `history_url` 配置全节点 REST |

```json
{
  "name": "solana",
  "protocol": "ws",
  "url": "wss://api.mainnet-beta.solana.com",
  "history_url": "https://api.mainnet-beta.solana.com"
}
```

**Ethereum Beacon（可选）。** 信标节点可以通过一条保持打开的 HTTP 响应推送头部通知，这种机制称为 SSE（Server-Sent Events）。将 `protocol` 设为 `sse`，`url` 指向信标节点的 `/eth/v1/events`，`history_url` 填以太坊执行层 HTTP RPC。扫描器订阅 `head` 主题，游标使用执行层区块高度。使用支持 WebSocket 的执行节点时，单靠该节点即可接收新区块头。

<a id="nodes"></a>

## 节点管理

<a id="list"></a>

### `GET /v1/admin/nodes`

可选查询参数 `network` 按链筛选。返回 **200** 和节点数组，包括上次保存的探测结果。列出清单不会请求上游 RPC。没有匹配节点时返回 `[]`。

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

| 字段 | 类型 | 行为 |
| --- | --- | --- |
| `id, network` | `int64, string` | 稳定的数值 ID 和具体网络。 |
| `protocol, url` | `string, string` | 选定的接口及节点 URL。 |
| `origin` | `string` | 来源可为 `builtin`、`config`、`custom`、`chainlist`、`openchainbench`、`comparenodes`、`fallback`。 |
| `status` | `string` | 可为 `untested`、`ok`、`timeout`、`unavailable`，探测后检查此字段。 |
| `height, height_unit` | `int64? , string` | 上游高度或 null。单位可为 `block`、`slot`、`checkpoint`、`ledger_version`、`beacon_slot`。 |
| `checked_at_ms, latency_ms` | `int64?, int64?` | 探测开始的 Unix 毫秒时间戳和耗时毫秒数。首次探测前均为 null。 |
| `read_failure, read_retry_at_ms` | `string?, int64?` | 读块失败时，扫描节点显示 `status: unavailable` 并给出失败原因。查高度成功会更新高度，读块成功才清除失败记录。冷却期间还会返回截止时间（Unix 毫秒）。 |
| `scannable` | `bool` | 节点能否扫描完整交易。仅能查询高度的端点不会收到扫块任务。 |

<a id="add"></a>

### `POST /v1/admin/nodes`

JSON 字符串 `network`、`url` 标识网络与节点，建议显式发送 `protocol` 指定接口。可选字符串有 `api_key`、`history_url`、`history_api_key`；配对协议必须提供单独的历史 URL。未知字段、错误选项或非法 JSON 返回 **400**，正文上限 8 KiB。创建成功返回 **201** 和节点对象，已存在返回 **409**。添加节点不会单独启用某条链的扫描线程。

```sh
admin -X POST "$BASE/v1/admin/nodes" --data '{"network":"base","protocol":"http","url":"https://rpc.example.org"}'
```

<a id="fallback"></a>

### `POST /v1/admin/nodes/fallback`

使用管理员令牌为指定链登记付费历史节点，参数与普通节点相同。成功返回 **201**，节点的 `origin` 为 `fallback`。普通节点读块失败后，空闲的兜底节点可立即接手该区间；已记录到 SQLite 的缺块在重启后也可用它补扫。补扫前会核对该节点自身的高度与确认数。新块首扫、订阅边界查询和日常链头轮询均使用普通节点。管理员主动测试（含批量测试）会请求付费节点。补扫再次失败会保留缺块，节点同样遵循连续三次失败后的阶梯冷却。可用返回的节点 ID 查询列表、测试或删除。

```sh
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://second-rpc.example.net","api_key":"SECOND_PROVIDER_KEY"}'
```

<a id="scan-progress"></a>

### `GET /v1/admin/progress`

每条已配置网络返回一行。查看进度只读现有数据。`target_height` 是满足确认深度的目标高度，`cursor_height` 是连续扫完的高度。`lag_ms` 表示扫描器首次观测到尚未连续扫完的目标后，至少经过了多少毫秒。每个监听周期最多保留 361 个间隔至少 5 秒的样本，重启后重新观测。超过 300,000 毫秒为 `warning`，否则为 `ok`。无订阅 `idle`、等待样本 `warming`、链头过期 `stale` 时延迟为 null。链头过期阈值取 5 分钟和两个轮询周期中的较长者。

`scan_success_count` 和 `scan_failure_count` 分别表示本次连续监听期间成功和失败的扫块区段次数。节点切换重试后每个区段只计一次结果，后续重新调度补扫会再计一次。高度查询、节点健康检查、空闲和取消均不计入。两个计数从零开始，在最后一个订阅结束或服务重启后归零。旧版服务省略这两个字段。

<a id="scan-retry-rate"></a>

### `GET /v1/admin/scan-retry-rate`

使用管理员令牌。省略 `network` 返回所有已配置网络，传一个网络名可单独查询。网络名为空、未配置或重复传参返回 **400**。

`total_attempts` 统计节点通过高度检查后实际开始的扫块区间读取次数。`retry_attempts` 统计其中的重读次数，包括换节点重读和对已标记失败区间的补扫。一次区间读取计一次，即使覆盖多个高度或调用多次 RPC。链头探测、按哈希查交易及通知重投均不计入。计数在内存中跨监听周期累积，服务重启后归零。`retry_rate` 等于重试次数除以总尝试次数，范围为 0 到 1，尚无读取时为 `null`。要统计同一次运行内某段时间的重试率，可用两次查询之间的重试增量除以总尝试增量。

```sh
admin "$BASE/v1/admin/scan-retry-rate?network=base"
```

```json
[{"network":"base","total_attempts":100,"retry_attempts":4,"retry_rate":0.04}]
```

<a id="test-one"></a>

### `POST /v1/admin/nodes/{id}/test`

路径 `id` 为正整数，不传正文。返回 **200** 和更新后的节点；探测失败也可返回 200，此时状态为 `timeout` 或 `unavailable`。无效 ID 返回 **400**，不存在或已移除的 ID 返回 **404**。

```sh
NODE_ID=$(curl -fsS -H "Authorization: Bearer $ADMIN" "$BASE/v1/admin/nodes?network=base" | jq -er '.[] | select(.url == "https://rpc.example.org" and .origin == "custom") | .id') &&
admin -X POST "$BASE/v1/admin/nodes/$NODE_ID/test"
```

<a id="test-all"></a>

### `POST /v1/admin/nodes/test`

探测所有可见节点，可用 `network` 按链筛选，不传正文。返回 **200** 和节点数组，包含探测失败项。最多并行探测八个节点；另一批次正在执行时返回 **409**。客户端取消请求会中止本批探测。

```sh
admin -X POST "$BASE/v1/admin/nodes/test?network=base"
```

<a id="delete"></a>

### `DELETE /v1/admin/nodes/{id}`

按正整数 `id` 移除节点，包括内置节点和配置节点。成功返回 **204**，无效 ID 返回 **400**，节点不存在返回 **404**。文件模式下重启后仍保留删除记录。重新提交节点可以恢复该条目。

```sh
admin -X DELETE "$BASE/v1/admin/nodes/$NODE_ID"
```

<a id="blacklist"></a>

### 节点黑名单

使用管理员令牌。域名规则覆盖该域名及其全部子域名，跨端口、跨网络生效；IP 规则精确匹配该地址。节点发现探测和节点选择都会检查登记的主节点与配套历史节点 URL。规则按 URL 主机匹配，无法识别 DNS 别名、反向代理或 TON 全局配置背后的实际运营商。

`keyword:acme` 按主机名做不区分大小写的子串匹配；`exact:rpc.example.org` 只封禁完整主机名；直接填写域名则同时封禁子域名。配置文件和添加、移除接口使用同一种写法。关键词不匹配 URL 路径、查询参数或凭据。

```sh
admin -X PUT "$BASE/v1/admin/blacklist/keyword:acme"
admin -X PUT "$BASE/v1/admin/blacklist/exact:rpc.example.org"
admin "$BASE/v1/admin/blacklist"
admin -X DELETE "$BASE/v1/admin/blacklist/keyword:acme"
admin -X DELETE "$BASE/v1/admin/blacklist/exact:rpc.example.org"
```

列表按主机名排序，格式为 `[{"host":"rpc.example.org","source":"admin"}]`。添加和删除可重复调用，均返回 204。规则用于新的节点选择，运行中的扫描器会刷新节点池。文件型 SQLite 会保留 API 规则。也可在服务配置顶层写入 `"blacklist": ["rpc.example.org"]` 固定规则；此类规则来源为 `config`，API 删除返回 409，需修改配置文件并重启。

<a id="errors"></a>

## 错误处理

遇到 **400** 先修正参数；**401** 时检查 Bearer 令牌及角色。**409** 的含义随接口而异，见各接口说明。**503** 表示订阅、队列、游标或节点存储暂时无法完成操作，可退避重试。空队列返回 **204**。

[使用指南](../../README.zh-CN.md) · [ConsumerHandler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/feed/handler.go) · [NodeRegistry.Handler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/nodes/http.go)

### 节点管理能力

通过 `GET /v1/admin/capabilities` 查询支持的协议及节点配置和迁移能力。`PATCH /v1/admin/nodes/{id}` 支持修改 `enabled`、`role`、`purpose`、`weight`、`url`、`api_key`、`history_url` 和 `history_api_key`。省略的字段保留原值。角色分为普通节点（regular）和兜底节点（fallback）。停用的节点保留原配置，重启和重新发现后仍然停用。普通节点可以设置为补单专用（manual_verify）。人工严格验单使用 `manual=true`。批量健康检查跳过停用节点，传入 `include_manual=true` 可包含补单专用节点。
