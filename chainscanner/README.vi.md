<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Theo dõi địa chỉ trên chuỗi và nhận giao dịch qua WebSocket, gRPC hoặc HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>Tiếng Việt</strong> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">AGPL-3.0</a> &nbsp;·&nbsp; 18 mạng</p>
<p align="center"><a href="docs/guide/vi.md#quick-start">Khởi động nhanh</a> &nbsp;·&nbsp; <a href="docs/guide/vi.md#networks">Mạng hỗ trợ</a> &nbsp;·&nbsp; <a href="docs/api/vi.md">Tài liệu API</a></p>

---

## Vì sao chúng tôi xây dựng ChainScanner

Khi phát triển GM Wallet và GMPay, chúng tôi thường gặp cùng một vấn đề. Ví cần biết địa chỉ nào cần cập nhật số dư, còn dịch vụ thanh toán cần phát hiện giao dịch mới tại các địa chỉ nhận tiền. Nếu mỗi ứng dụng liên tục truy vấn RPC cho từng địa chỉ, cả hai sẽ tốn hạn mức để phát hiện cùng một giao dịch.

ChainScanner quét khối trên từng mạng một lần cho tất cả các dịch vụ đăng ký. Mỗi dịch vụ nhận những giao dịch liên quan đến địa chỉ mình theo dõi và xác nhận đã xử lý một cách độc lập. Ví có thể chỉ cập nhật số dư của các địa chỉ bị ảnh hưởng, còn GMPay có thể xác minh đơn hàng tương ứng. Khi đọc lại cùng bốn khối trên mạng chính Ethereum, số yêu cầu RPC đến nhà cung cấp vẫn là 11 dù theo dõi một địa chỉ, theo dõi 100 địa chỉ hay có hai dịch vụ cùng theo dõi 100 địa chỉ đó.

Mỗi ứng dụng tự quản lý logic nghiệp vụ, trạng thái đơn hàng và số dư ví. ChainScanner cung cấp hạ tầng dùng chung để đăng ký theo dõi địa chỉ, quét khối và chuyển sự kiện đến các dịch vụ đã đăng ký.

> [!IMPORTANT]
> Tra cứu theo mã băm ưu tiên nút dự phòng, loại các nút đã cung cấp giao dịch khi quét khối và trả giao dịch hợp lệ đầu tiên. [So sánh các nhà cung cấp nút dự phòng](#fallback-providers)

## Tình huống sử dụng

- **Cổng thanh toán.** [Tích hợp EPUSDT](https://github.com/GMWalletApp/epusdt/pull/127) theo dõi địa chỉ nhận tiền và xác minh giao dịch qua truy vấn nghiêm ngặt trước khi ghi có.
- **Ý tưởng bot Telegram.** Bot có thể lưu sự kiện ví cá nhân rồi thông báo tiền vào hoặc ra kèm mã giao dịch.
- **Ý tưởng đối soát.** Kế toán và dịch vụ cảnh báo theo dõi cùng nhóm ví và xác nhận xử lý độc lập.

## Đăng ký một địa chỉ

Sau khi chạy máy chủ cục bộ, dùng `$TOKEN` ở phần khởi động nhanh và thay địa chỉ mẫu bằng địa chỉ Base của bạn. Yêu cầu này thay toàn bộ danh sách theo dõi của ứng dụng.

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v1/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

Trường `from_height` trong phản hồi cho biết khối đầu tiên được theo dõi. [Nhận và xác nhận giao dịch](docs/api/vi.md#streaming)

## Nút dự phòng được khuyến nghị

Đăng ký riêng từng nút dự phòng. Các nút đủ điều kiện luân phiên quét bù các khoảng bị lỗi. [API](docs/api/vi.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

<a id="fallback-providers"></a>

### Nhà cung cấp nút dự phòng

Kiểm tra ngày 2026-11-21. Bạn tự lựa chọn nhà cung cấp. ChainScanner không bảo đảm độ sẵn sàng, độ chính xác hay giá. Credits, CU và RU phụ thuộc vào phương thức RPC. Hãy kiểm tra mạng, dữ liệu lịch sử và giao thức.

| Trang web | Thành lập hoặc ra mắt và lĩnh vực | Hạn mức miễn phí |
| --- | --- | --- |
| [Ankr](https://www.ankr.com/) | [2017 · hạ tầng nút phân tán](https://www.ankr.com/ankr-whitepaper-2.0.pdf) | [200 triệu API credits/tháng với tài khoản Freemium](https://www.ankr.com/docs/rpc-service/charging-policy/) |
| [Infura](https://www.infura.io/) | [2016 · hạ tầng Ethereum và IPFS của Consensys](https://consensys.io/blog/press-release-infura-a-consensys-company-launches-infura-for-improved-ethereum-infrastructure-support) | [3 triệu credits/ngày với Core theo tài liệu tính phí](https://docs.infura.io/get-started/pricing/) |
| [PublicNode](https://www.publicnode.com/) | [2018 · nhà vận hành Allnodes bắt đầu cung cấp hạ tầng nút](https://www.allnodes.com/about/) | [RPC công cộng dùng chung, chưa công bố hạn mức cố định theo tháng cho từng người](https://www.publicnode.com/) |
| [QuickNode](https://www.quicknode.com/) | [2017 · RPC đa chuỗi và luồng dữ liệu](https://www.quicknode.com/about) | [10 triệu API credits cho 30 ngày dùng thử](https://support.quicknode.com/articles/5387074982-1-month-free-trial) |
| [Alchemy](https://www.alchemy.com/) | [2017 · API phát triển blockchain và truy cập nút](https://www.alchemy.com/company) | [30 triệu CU/tháng](https://www.alchemy.com/docs/reference/pricing-plans) |
| [BlockPI](https://blockpi.io/) | [2022-10-25 · ra mắt bản alpha của mạng RPC phân tán](https://docs.blockpi.io/welcome-to-blockpi/about-blockpi) | [50 triệu RU/tháng cho tài khoản đăng ký](https://docs.blockpi.io/pricing/pricing-and-rate-limit) |
| [dRPC](https://drpc.org/) | [2023-03 · mở cổng RPC phi tập trung cho công chúng](https://blog.drpc.org/how-drpcs-free-tier-works/) | [50 triệu CU mỗi 30 ngày](https://drpc.org/docs/howitworks/ratelimiting) |

## Lời cảm ơn

Nếu dự án của bạn dùng ChainScanner hoặc tham khảo thiết kế của chúng tôi, mong bạn nhắc đến ChainScanner và thêm liên kết trong README của dự án. Lời giới thiệu của bạn có ý nghĩa rất lớn với nhóm và giúp các lập trình viên khác tìm thấy dự án. Chúng tôi rất vui vì đã giúp được bạn. Cảm ơn bạn đã dùng ChainScanner!

```markdown
Nguồn tham khảo: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```

Cảm ơn imToken và Bitget Wallet đã cung cấp RPC công cộng, cùng ChainList, OpenChainBench và CompareNodes đã duy trì danh mục nút. Cảm ơn mọi người đã sử dụng và tin tưởng ChainScanner.
