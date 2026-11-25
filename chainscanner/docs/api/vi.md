# ChainScanner · Tài liệu API

[English](en.md) · [简体中文](zh-CN.md) · **Tiếng Việt** · [Español](es.md) · [Русский](ru.md)

[Trang chủ](../../README.vi.md) · [Khởi động nhanh](../guide/vi.md#quick-start) · [Kết nối và xác thực](#auth)  ·  [Thay danh sách đăng ký](#subscriptions)  ·  [Đọc và xác nhận sự kiện](#deliveries)  ·  [Đăng ký và kết nối dài hạn](#streaming)  ·  [Quản lý nút](#nodes)  ·  [Xử lý lỗi](#errors)

Dịch vụ dùng token riêng để đăng ký, đọc và xác nhận sự kiện. Quản lý nút bằng token quản trị riêng. Các ví dụ dùng máy chủ cục bộ trong hướng dẫn khởi động nhanh.

<a id="auth"></a>

## Kết nối và xác thực

Gửi `Authorization: Bearer <token>` trong mọi yêu cầu. Token xác định dịch vụ và hàng đợi của dịch vụ đó. Mỗi token phải khác nhau và dài ít nhất 24 byte. Đặt `admin_token` để bật các tuyến quản trị. Gửi JSON với `Content-Type: application/json`. Phản hồi thành công có nội dung dùng JSON; HTTP 204 không có nội dung. Lỗi thường trả văn bản thuần.

```sh
BASE=http://127.0.0.1:9081
TOKEN=$(jq -r '.consumers.payments' chainscanner.json)
ADMIN=$(jq -r '.admin_token' chainscanner.json)
consumer() { curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
admin() { curl -sS -i -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' "$@"; }
```

<a id="routes"></a>

## Danh sách API

| Vai trò | HTTP | Thành công |
| --- | --- | --- |
| Dịch vụ | [`PUT /v1/subscriptions`](#replace-watches) | `200 / 202` |
| Dịch vụ | [`GET /v1/events/next`](#next) | `200 / 204` |
| Dịch vụ | [`GET /v1/events/stream`](#streaming) · WebSocket | `101` |
| Dịch vụ | [`POST /v1/events/{id}/ack`](#ack) | `204` |
| Dịch vụ | [`GET /v1/heights/{network}`](#height) | `200` |
| Dịch vụ | [`GET /v1/transactions/{network}/{hash}`](#transactions) | `200` |
| Quản trị viên | [`GET /v1/admin/progress`](#scan-progress) | `200` |
| Quản trị viên | [`GET /v1/admin/scan-retry-rate`](#scan-retry-rate) | `200`, `400` |
| Quản trị viên | [`GET /v1/admin/nodes`](#list) | `200` |
| Quản trị viên | [`POST /v1/admin/nodes`](#add) | `201` |
| Quản trị viên | [`POST /v1/admin/nodes/fallback`](#fallback) | `201` |
| Quản trị viên | [`POST /v1/admin/nodes/{id}/test`](#test-one) | `200` |
| Quản trị viên | [`POST /v1/admin/nodes/test`](#test-all) | `200` |
| Quản trị viên | [`DELETE /v1/admin/nodes/{id}`](#delete) | `204` |
| Quản trị viên | [`GET /v1/admin/blacklist`](#blacklist) | `200` |
| Quản trị viên | [`PUT /v1/admin/blacklist/{host}`](#blacklist) | `204` |
| Quản trị viên | [`DELETE /v1/admin/blacklist/{host}`](#blacklist) | `204` |

<a id="transactions"></a>

## Tra cứu giao dịch theo mã băm

Gửi mã xác thực của dịch vụ và ID mạng đã cấu hình. Không cần đăng ký địa chỉ. Phản hồi chứa `network`, `hash` và `result`.

Tra cứu theo mã băm ưu tiên nút dự phòng, loại các nút đã cung cấp giao dịch khi quét khối và trả giao dịch hợp lệ đầu tiên.

HTTP 404 kèm `X-ChainScanner-Evidence: not_found` nghĩa là nút đã trả lời rằng không tìm thấy giao dịch. Có thể tạm dừng đơn hàng để kiểm tra thủ công. Với 502 hoặc 503, hãy thử lại sau một khoảng chờ.

Nếu truy vấn lỗi, hệ thống chuyển sang nút khác và thử lại ít nhất ba lần khi còn nút thay thế và thời gian. Dùng `?fallback_only=true` để chỉ dùng nút dự phòng. Với `?observed=true`, cần có bản ghi nguồn quét. Nếu thiếu bản ghi hoặc không còn nút độc lập, API trả về 503.

```sh
consumer "$BASE/v1/transactions/base/$TX_HASH"
consumer "$BASE/v1/transactions/base/$TX_HASH?fallback_only=true"
```

<a id="trust"></a>

### Khi nào cần xác minh giao dịch

Trước khi ghi có, hãy kiểm tra kết quả thực thi, độ sâu xác nhận, người nhận, tài sản và số tiền.

[Danh sách đen nút](#blacklist)

<a id="subscriptions"></a>

## Thay danh sách đăng ký

Gửi **toàn bộ** danh sách theo dõi của dịch vụ dưới dạng mảng JSON (tối đa 8 MiB). Mỗi phần tử có chuỗi `network` và `address` cho đúng mạng và địa chỉ hợp lệ. Địa chỉ cũ bị bỏ khỏi danh sách sẽ được hủy đăng ký. Mảng rỗng hủy toàn bộ nhưng giữ các sự kiện đã xếp hàng.

<a id="replace-watches"></a>

### `PUT /v1/subscriptions`

Mỗi đăng ký có mốc đầu `from_height`, bao gồm cả khối đó. Trên mạng đang chạy, địa chỉ mới bắt đầu sau khối cao nhất đã giao. Khi hủy, `to_height` bao gồm khối cao hơn giữa đầu chuỗi vừa đọc và khối cao nhất đã giao. Dữ liệu không hợp lệ trả **400**. Nếu không đọc được độ cao cần thiết, **409** giữ nguyên toàn bộ danh sách trước đó.

```sh
consumer -X PUT "$BASE/v1/subscriptions" --data '[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]'
```

```json
{"registrations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101}],"terminations":[]}
```

Mã **202** cho biết ít nhất một địa chỉ đang chờ xử lý các khối cuối. Gửi lại đúng danh sách đầy đủ để kiểm tra tiến độ với cùng mốc cuối. Mã **200** cho biết tất cả lượt hủy trong phản hồi đã hoàn tất: các khối đã đủ xác nhận, khoảng trống đã được quét bù và sự kiện đã vào hàng đợi. Dịch vụ vẫn có thể nhận và xác nhận sự kiện sau đó.

```json
{"registrations":[],"terminations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101,"to_height":110,"completed":false}]}
```

<a id="deliveries"></a>

## Đọc và xác nhận sự kiện

<a id="next"></a>

### `GET /v1/events/next`

Tham số truy vấn bắt buộc `network` chọn một mạng được hỗ trợ. `wait` là số nguyên tùy chọn 1–20 giây để chờ dài; bỏ qua để trả ngay. Có sự kiện trả **200**, chưa có trả **204**. Thiếu mạng, mạng không hợp lệ hoặc thời gian chờ sai trả **400**.

```sh
consumer "$BASE/v1/events/next?network=base&wait=15"
```

Ví dụ minh họa cấu trúc sự kiện với dữ liệu giao dịch rút gọn. Một giao dịch khớp nhiều địa chỉ chỉ tạo một lần giao cho mỗi dịch vụ. Giao dịch đến, đi hoặc thất bại đều có thể khớp. Cần kiểm tra chiều chuyển, kết quả, tài sản và số tiền trước khi ghi có.

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

| Trường | Kiểu | Ý nghĩa |
| --- | --- | --- |
| `id` | `int64` | ID lần giao dùng trong đường dẫn xác nhận. |
| `activity.network, activity.height` | `string, int64` | Mạng cụ thể và vị trí trên sổ cái. |
| `activity.tx_id` | `string` | Định danh giao dịch trên chuỗi. |
| `activity.confirmations, activity.time_ms` | `int64, int64` | Số xác nhận quan sát được và thời gian trên chuỗi tính bằng mili giây Unix. |
| `activity.addresses` | `string[]` | Các địa chỉ đăng ký của dịch vụ đã khớp. |
| `activity.data` | `JSON` | Dữ liệu JSON riêng của mạng. EVM có thể chứa `transaction`, `logs`, `receipt` và `trace`. |
| `activity.data.refresh_assets` | `string[]` | Danh sách hợp đồng token cần cập nhật số dư sau sự kiện toàn cục đã nhận diện. Bỏ qua trường này nếu danh sách rỗng. [EVM](../guide/en.md#asset-events) |

<a id="ack"></a>

### `POST /v1/events/{id}/ack`

Đặt `id` dương của sự kiện trong đường dẫn, không gửi nội dung. Xác nhận chỉ áp dụng cho dịch vụ hiện tại và trả **204**, kể cả khi lặp lại xác nhận thành công. ID sai hoặc không dương trả **400**. Đọc không xóa sự kiện. Hãy lưu kết quả trước khi xác nhận và chống trùng theo mạng cùng mã giao dịch hoặc lần chuyển tiền.

```sh
ID=17
consumer -X POST "$BASE/v1/events/$ID/ack"
```

<a id="height"></a>

### `GET /v1/heights/{network}`

Trả **200** với số nguyên JSON biểu thị độ cao liên tục cao nhất đã quét xong, hoặc `-1` nếu chưa có con trỏ. Khi còn khối thiếu, sự kiện đã giao có thể ở độ cao lớn hơn. Aptos dùng phiên bản sổ cái, Solana dùng slot và Sui dùng checkpoint.

```sh
consumer "$BASE/v1/heights/base"
```

```json
12345677
```

<a id="streaming"></a>

## Đăng ký và kết nối dài hạn

### Từ ChainScanner đến ứng dụng

Ứng dụng có thể mở kết nối WebSocket hoặc luồng gRPC hai chiều để nhận sự kiện ngay khi giao dịch khớp địa chỉ vào hàng đợi. Cả hai dùng chung token, danh sách địa chỉ và trạng thái xác nhận với HTTP long polling. Mỗi cặp dịch vụ và mạng chỉ nên chạy một trình đọc. Các dịch vụ khác nhau có thể đăng ký cùng địa chỉ một cách độc lập.

**Kết nối.** Tại thư mục gốc của kho mã, chọn một trong hai lệnh dưới đây và dùng token ở phần khởi động nhanh. WebSocket dùng HTTP/1.1 Upgrade cùng các khung văn bản JSON. gRPC dùng HTTP/2 trên cùng cổng với lược đồ protobuf của dự án. Nhập từng thông điệp trong phiên tương tác và giữ luồng nhập mở. Khi triển khai từ xa, dùng WSS hoặc gRPC qua TLS và cấu hình proxy chuyển tiếp WebSocket Upgrade cùng luồng gRPC HTTP/2.

[activity.proto](../../api/activity/v1/activity.proto)

```sh
websocat -H="Authorization: Bearer $TOKEN" ws://127.0.0.1:9081/v1/events/stream
```

```sh
grpcurl -plaintext -import-path . -proto api/activity/v1/activity.proto -H "authorization: Bearer $TOKEN" -d @ 127.0.0.1:9081 chainscanner.activity.v1.ActivityFeed/Subscribe
```

**Mở luồng → nhận biên nhận.** Khung đầu tiên phải ghi tên một mạng đã cấu hình. Chỉ gửi `replace` khi muốn thay toàn bộ danh sách địa chỉ của dịch vụ trên **tất cả các mạng**. Danh sách rỗng sẽ xóa toàn bộ đăng ký. Khi kết nối lại, bỏ `replace` để giữ danh sách hiện tại. Máy chủ trả về `ready`. Khi thay danh sách, phản hồi chứa địa chỉ chuẩn hóa và độ cao bắt đầu, tính cả độ cao đó. Khi nối lại, biên nhận rỗng.

Khi thay danh sách qua luồng, giới hạn nằm trong `ready.terminations`. Trong ProtoJSON, `toHeight` là chuỗi thập phân và nếu thiếu `completed` thì giá trị là false.

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

**Nhận → ghi bền vững → xác nhận.** Mỗi luồng chỉ có một sự kiện đang chờ xác nhận. Giải mã `dataJson` theo cấu trúc của mạng rồi lưu kết quả nghiệp vụ. Sau đó gửi `ack` với đúng ID vừa nhận trên cùng kết nối. Sự kiện tiếp theo sẽ đến khi có dữ liệu. ProtoJSON biểu diễn số nguyên 64 bit bằng chuỗi thập phân. Hàng đợi rỗng không phát thông điệp ứng dụng.

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

**Mất kết nối và lỗi.** Kết nối lại với thời gian chờ tăng dần rồi gửi lại `open`. Sự kiện chưa xác nhận có thể được gửi lại, kể cả qua giao thức khác. Khử trùng lặp theo mạng và mã giao dịch hoặc khoản chuyển. Token sai trả HTTP 401 khi bắt tay WebSocket hoặc `UNAUTHENTICATED` với gRPC. Mạng sai hoặc ID xác nhận không khớp trả 1008 hoặc `INVALID_ARGUMENT`. Chưa xác định được độ cao bắt đầu trả 1013 hoặc `FAILED_PRECONDITION`. Lỗi lưu trữ tạm thời trả 1013 hoặc `UNAVAILABLE`. Đặt giới hạn kích thước nhận của máy khách gRPC đủ lớn cho dữ liệu giao dịch dự kiến.

```json
{"open":{"network":"base"}}
```

Máy khách WebSocket phải trả lời khung Ping bằng Pong. Hãy dùng máy khách backend có thể đặt tiêu đề Authorization. Hàm khởi tạo WebSocket gốc của trình duyệt không cho đặt tiêu đề này. Với gRPC, giữ chiều gửi mở để xác nhận và hủy RPC khi muốn dừng luồng đang rỗi. Máy chủ Go nhúng cần bật HTTP/2 để phục vụ gRPC. Chương trình độc lập đã bật HTTP/2 không mã hóa trên cổng lắng nghe.

### HTTP long polling

Ứng dụng nhận giao dịch khớp địa chỉ qua HTTP long polling. Sau khi đăng ký toàn bộ danh sách địa chỉ, chạy một vòng đọc cho mỗi cặp dịch vụ và mạng. Đặt thời gian chờ của máy khách và proxy lớn hơn `wait`. Ví dụ dùng token ở phần khởi động nhanh, Python 3 và SQLite cục bộ. Khi nhận 204, chương trình gửi yêu cầu tiếp theo ngay. Khi có sự kiện, chương trình ghi dữ liệu thành công rồi mới xác nhận.

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

Ví dụ lưu một bản ghi cho mỗi giao dịch trên mỗi mạng của dịch vụ này. Nếu xử lý từng khoản chuyển trong cùng giao dịch, hãy thêm chỉ số tương ứng trên chuỗi vào khóa chống trùng. Mất phản hồi xác nhận có thể khiến sự kiện được gửi lại. Khi bộ quét chỉ dùng bộ nhớ khởi động lại, danh sách đăng ký và hàng đợi sẽ mất. Hãy đăng ký lại toàn bộ danh sách và đối soát khoảng trống bằng vị trí đã lưu cùng lịch sử trên chuỗi. SQLite dạng tệp giữ lại danh sách và hàng đợi sau khi khởi động lại.

### Từ nút mạng đến ChainScanner

Chọn giao thức nút theo từng mạng. Khi nhận thông báo về đầu chuỗi mới, bộ quét đọc dữ liệu trên chuỗi. Sau khi mất kết nối, bộ quét dựa vào độ cao đã hoàn tất liên tục để đọc bù. Khi nguồn còn hoạt động, luồng sẽ kết nối lại sau 5 giây. Nếu mạng không có địa chỉ được theo dõi, bộ quét của mạng đó tạm nghỉ.

| Mạng | Thông báo tiến độ | Nguồn dữ liệu lịch sử |
| --- | --- | --- |
| Các mạng EVM | WebSocket JSON-RPC · `eth_subscribe("newHeads")` | RPC thực thi trên cùng nút |
| Avalanche | WebSocket C-Chain chính thức · `newHeads` | Nút HTTP đi kèm xác minh ID mạng `43114` và đọc khối, giao dịch |
| Solana | WebSocket · `rootSubscribe` hoặc luồng slot đã hoàn tất của Yellowstone gRPC | HTTP JSON-RPC trong `history_url` |
| Sui | gRPC · `SubscribeCheckpoints` | Đọc checkpoint từ cùng nút gRPC |
| Aptos | Luồng giao dịch RawData gRPC | REST của fullnode trong `history_url` |

```json
{
  "name": "solana",
  "protocol": "ws",
  "url": "wss://api.mainnet-beta.solana.com",
  "history_url": "https://api.mainnet-beta.solana.com"
}
```

**Ethereum Beacon (tùy chọn).** Nút Beacon có thể đẩy thông báo đầu chuỗi qua một phản hồi HTTP giữ mở bằng Server-Sent Events (SSE). Đặt `protocol` là `sse`, `url` là endpoint `/eth/v1/events` của nút và `history_url` là HTTP RPC của lớp thực thi Ethereum. Bộ quét đăng ký chủ đề `head` và ghi vị trí theo độ cao khối thực thi. Nút thực thi hỗ trợ WebSocket cũng có thể tự cung cấp thông báo đầu chuỗi.

<a id="nodes"></a>

## Quản lý nút

<a id="list"></a>

### `GET /v1/admin/nodes`

Tham số tùy chọn `network` lọc theo mạng. Trả **200** với mảng nút và kết quả kiểm tra đã lưu. Việc liệt kê không gọi RPC; kết quả rỗng là `[]`.

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

| Trường | Kiểu | Ý nghĩa |
| --- | --- | --- |
| `id, network` | `int64, string` | ID số ổn định và mạng cụ thể. |
| `protocol, url` | `string, string` | Giao thức và URL nút. |
| `origin` | `string` | Nguồn: `builtin`, `config`, `custom`, `chainlist`, `openchainbench`, `comparenodes`, `fallback`. |
| `status` | `string` | Một trong `untested`, `ok`, `timeout`, `unavailable`. Kiểm tra trường này sau khi thăm dò. |
| `height, height_unit` | `int64? , string` | Độ cao của nút hoặc null. Đơn vị là `block`, `slot`, `checkpoint`, `ledger_version` hoặc `beacon_slot`. |
| `checked_at_ms, latency_ms` | `int64?, int64?` | Thời điểm bắt đầu theo mili giây Unix và thời lượng theo mili giây. Cả hai là null nếu chưa kiểm tra. |
| `read_failure, read_retry_at_ms` | `string?, int64?` | Khi đọc khối thất bại, trạng thái là `unavailable`. Kiểm tra độ cao thành công vẫn giữ lý do lỗi cho đến khi đọc khối thành công. Thời điểm hết tạm dừng tính bằng mili giây Unix. |
| `scannable` | `bool` | Cho biết nút có thể quét giao dịch hay không. Nút chỉ báo độ cao sẽ không nhận tác vụ quét. |

<a id="add"></a>

### `POST /v1/admin/nodes`

Dùng các chuỗi JSON `network`, `url` và chỉ rõ `protocol`. Các chuỗi tùy chọn là `api_key`, `history_url`, `history_api_key`; giao thức ghép cần một URL lịch sử riêng. Trường lạ, tùy chọn không hợp lệ hoặc JSON lỗi trả **400**. Nội dung tối đa 8 KiB. Trả **201** kèm nút, hoặc **409** nếu đã tồn tại. Thêm nút không tự bật bộ quét của mạng.

```sh
admin -X POST "$BASE/v1/admin/nodes" --data '{"network":"base","protocol":"http","url":"https://rpc.example.org"}'
```

<a id="fallback"></a>

### `POST /v1/admin/nodes/fallback`

Đăng ký nút lịch sử trả phí cho một mạng bằng các trường và token quản trị như khi thêm nút thường. Phản hồi **201** có `origin: fallback`. Khi đọc khối thất bại, nút dự phòng đang rảnh có thể nhận khoảng đó ngay và kiểm tra độ cao xác nhận. Nút này cũng quét bù các khoảng lỗi đã lưu trong SQLite sau khi khởi động lại. Nút thường đảm nhiệm khối mới, mốc đăng ký và việc kiểm tra đầu chuỗi định kỳ. Kiểm tra thủ công có thể dùng hạn mức trả phí. Lỗi quét bù giữ nguyên khoảng cần thử lại, ba lỗi liên tiếp kích hoạt thời gian tạm dừng tăng dần. Dùng ID trả về để kiểm tra hoặc xóa nút. Đăng ký riêng từng nút dự phòng. Các nút đủ điều kiện luân phiên quét bù các khoảng bị lỗi.

```sh
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://second-rpc.example.net","api_key":"SECOND_PROVIDER_KEY"}'
```

<a id="scan-progress"></a>

### `GET /v1/admin/progress`

Trả về một dòng cho mỗi mạng đã cấu hình mà không gọi RPC. `target_height` đã tính độ sâu xác nhận, còn `cursor_height` là độ cao đã quét liên tục. `lag_ms` là thời gian chờ tối thiểu đã quan sát của một độ cao đủ điều kiện nhưng chưa quét xong. Dữ liệu quan sát được làm mới khi khởi động lại hoặc bắt đầu đợt theo dõi mới. Trạng thái là `warning` khi vượt 300.000 ms, ngược lại là `ok`. Độ trễ là null với `idle`, `warming` và `stale`.

`scan_success_count` và `scan_failure_count` đếm số dải khối quét thành công và thất bại trong đợt theo dõi liên tục hiện tại. Mỗi dải có một kết quả sau khi thử các nút thay thế. Lần quét bù sau đó được tính riêng. Truy vấn chiều cao, kiểm tra nút, thời gian chờ và tác vụ bị hủy không tạo mẫu. Hai bộ đếm trở về 0 khi kết thúc đăng ký cuối cùng hoặc khởi động lại máy quét. Các phiên bản cũ không có hai trường này.

<a id="scan-retry-rate"></a>

### `GET /v1/admin/scan-retry-rate`

Dùng token quản trị viên. Bỏ qua `network` để xem mọi mạng đã cấu hình, hoặc truyền tên một mạng. Tên rỗng, mạng chưa cấu hình hoặc tham số lặp lại trả về **400**.

`total_attempts` đếm số lượt bắt đầu đọc dải khối sau khi kiểm tra độ cao của nút. `retry_attempts` đếm lượt đọc lại và lượt quét bù dải đã đánh dấu lỗi, kể cả khi đổi nhà cung cấp. Một lượt chỉ tính một lần dù bao gồm nhiều độ cao hoặc nhiều lệnh RPC. Thăm dò độ cao, tra cứu giao dịch và gửi lại thông báo đều được loại trừ. Bộ đếm được giữ trong bộ nhớ qua các phiên theo dõi và đặt lại khi khởi động lại dịch vụ. `retry_rate` bằng `retry_attempts / total_attempts` (0–1), hoặc `null` khi chưa có lượt đọc.

```sh
admin "$BASE/v1/admin/scan-retry-rate?network=base"
```

```json
[{"network":"base","total_attempts":100,"retry_attempts":4,"retry_rate":0.04}]
```

<a id="test-one"></a>

### `POST /v1/admin/nodes/{id}/test`

`id` trong đường dẫn phải là số nguyên dương; không gửi nội dung. Trả **200** kèm nút đã cập nhật, kể cả khi kết quả là `timeout` hoặc `unavailable`. ID không hợp lệ trả **400**; ID không tồn tại hoặc đã xóa trả **404**.

```sh
NODE_ID=$(curl -fsS -H "Authorization: Bearer $ADMIN" "$BASE/v1/admin/nodes?network=base" | jq -er '.[] | select(.url == "https://rpc.example.org" and .origin == "custom") | .id') &&
admin -X POST "$BASE/v1/admin/nodes/$NODE_ID/test"
```

<a id="test-all"></a>

### `POST /v1/admin/nodes/test`

Kiểm tra mọi nút hiển thị, có thể lọc bằng `network`. Không gửi nội dung. Trả **200** với mảng nút, gồm cả nút kiểm tra lỗi. Tối đa tám phép thử song song. Nếu một đợt khác đang chạy thì trả **409**. Hủy yêu cầu sẽ dừng đợt kiểm tra.

```sh
admin -X POST "$BASE/v1/admin/nodes/test?network=base"
```

<a id="delete"></a>

### `DELETE /v1/admin/nodes/{id}`

Xóa nút theo `id` nguyên dương, kể cả nút tích hợp hoặc đã cấu hình. Thành công trả **204**, ID sai trả **400**, không tìm thấy trả **404**. Chế độ dùng tệp giữ việc xóa sau khởi động lại. Có thể gửi lại đầu cuối để khôi phục.

```sh
admin -X DELETE "$BASE/v1/admin/nodes/$NODE_ID"
```

<a id="blacklist"></a>

### Danh sách đen nút

Dùng token quản trị. Quy tắc tên miền bao gồm các tên miền con trên mọi mạng và cổng. IP được so khớp chính xác. Hệ thống kiểm tra URL chính và URL lịch sử khi chọn nút hoặc thăm dò nút mới. Quy tắc không xác định nhà vận hành sau bí danh DNS, proxy hay cấu hình TON toàn cục.

`keyword:acme` khớp chuỗi con của tên máy chủ không phân biệt hoa thường, `exact:rpc.example.org` chỉ khớp đúng tên đó, còn miền không tiền tố bao gồm cả miền con. Cấu hình và cả hai API dùng cùng cú pháp. Đường dẫn, thông tin xác thực và tham số không được so khớp.

```sh
admin -X PUT "$BASE/v1/admin/blacklist/keyword:acme"
admin -X PUT "$BASE/v1/admin/blacklist/exact:rpc.example.org"
admin "$BASE/v1/admin/blacklist"
admin -X DELETE "$BASE/v1/admin/blacklist/keyword:acme"
admin -X DELETE "$BASE/v1/admin/blacklist/exact:rpc.example.org"
```

GET trả về mảng `{host, source}` đã sắp xếp. PUT và DELETE có thể gọi lặp lại và trả về 204. Lần chọn nút mới áp dụng quy tắc và bộ quét cập nhật nhóm nút. SQLite dạng tệp giữ các quy tắc qua lần khởi động lại. Đặt `"blacklist": ["rpc.example.org"]` ở cấp gốc của cấu hình để cố định quy tắc. Xóa quy tắc cấu hình qua API sẽ trả về 409. Sửa tệp rồi khởi động lại để gỡ.

<a id="errors"></a>

## Xử lý lỗi

Sửa yêu cầu trước khi thử lại **400**. Với **401**, kiểm tra Bearer token và quyền của nó. Xem ý nghĩa **409** trong từng mục ở trên. **503** nghĩa là kho đăng ký, hàng đợi, con trỏ hoặc kho nút chưa xử lý được yêu cầu; thử lại với thời gian chờ tăng dần. Hàng đợi trống trả **204**.

[Hướng dẫn](../../README.vi.md) · [ConsumerHandler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/feed/handler.go) · [NodeRegistry.Handler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/nodes/http.go)
