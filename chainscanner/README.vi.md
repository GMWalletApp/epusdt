<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt=""></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Theo dõi địa chỉ trên chuỗi và nhận giao dịch qua WebSocket, gRPC hoặc HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>Tiếng Việt</strong> · <a href="README.es.md">Español</a> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 17 mạng</p>
<p align="center"><a href="docs/guide/vi.md#quick-start">Khởi động nhanh</a> &nbsp;·&nbsp; <a href="docs/guide/vi.md#networks">Mạng hỗ trợ</a> &nbsp;·&nbsp; <a href="docs/api/vi.md">Tài liệu API</a></p>

---

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

Với mỗi mạng nhận thanh toán, hãy đăng ký một nút tin cậy của nhà vận hành khác. Nút này có thể đọc lại khối bị lỗi và được ưu tiên cho một trong hai vị trí tra cứu nghiêm ngặt. Sau hai lượt thất bại, đơn chuyển sang kiểm tra thủ công. [API](docs/api/vi.md#fallback)

```sh
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

## Lời cảm ơn

Nếu dự án của bạn dùng ChainScanner hoặc tham khảo thiết kế của chúng tôi, mong bạn nhắc đến ChainScanner và thêm liên kết trong README của dự án. Lời giới thiệu của bạn có ý nghĩa rất lớn với nhóm và giúp các lập trình viên khác tìm thấy dự án. Chúng tôi rất vui vì đã giúp được bạn. Cảm ơn bạn đã dùng ChainScanner!

```markdown
Nguồn tham khảo: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```
