# zcode 任务：OKX OnchainOS 配置页第四轮整改（用户反馈，两点）

背景：第三轮已把 API Key、API Secret、Passphrase 合进一个「API 凭证」卡片并部署上线。用户看了页面又提两点，本轮改掉。**模型：默认 GLM-5.3-Flash，不要切旗舰。**

## 1. 删掉 Passphrase 下面那句说明
- 删掉这句话（整句，一个字不留）：
  > 创建 API Key 时你自己设置的密码短语，不是 OKX 生成的；每次签名请求都会随 Key、Secret 一同校验，丢失后只能删除 Key 重建。
- 只删这句说明文字，Passphrase 字段本身、卡片、其他文案都不动。删完检查线上 chunk 里搜不到「丢失后只能删除 Key 重建」。

## 2. API 地址改为固定值，不再让用户设置
- 现状：页面上有个「API 地址」输入框，值是 `https://web3.okx.com`，用户可以改。用户认为这不该由用户填，应该是固定好的。
- 要求：
  1. **页面**：删掉「API 地址」输入框，改成静态文字展示（不是输入框、不可编辑），例如「API 地址：https://web3.okx.com（固定）」，让用户知道请求发到哪里即可。表单的 zod schema、defaultValues、load、save payload 里同步移除 api_url，不要再读写它。
  2. **后端**：读 `src/model/service/xlayer_service.go` 等用到 `xlayer.api_url` 设置的地方，把 API 基础地址写死为常量 `https://web3.okx.com`，不再依赖 settings 里的 `xlayer.api_url` 键。settings 表里残留的 `xlayer.api_url` 键不用删（也不用去动它），只保证后端不再读它。
  3. 接口超时（秒）那格保持不动。

## 构建与部署（必须走标准顺序）
重建顺序：`git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → `build-xlayer-admin-integration.py` → `rename-xlayer-chunks.py` → `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`（`src/` 目录下；若报 `go: not found`，把 `~/go/bin` 加进 PATH 再跑）。改过的 JS 全部 `node --check`。go 受影响包测试 + `go vet`。
部署：`instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`（沙箱内 sha256 核对）→ `docker cp /data/home/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`（instabox 命令在 `~/workspace/bin/instabox`）。重启前确认沙箱 `epusdt` 是 Running。
curl 验证：`/` 与 `/sign-in` 200；`/payments/gmpay/v1/config` 200 且 `xlayer.allow_tokens` 仍为 `["USDC","USDT0","USDG"]`、`xlayer.enabled` 为 true；改动 chunk 全部 200；线上 xlayer chunk 里搜不到「丢失后只能删除 Key 重建」和 `50125`/`50111`/`常见报错`。`~/xlayer-create-only.py 10` 建一笔 ¥10 测试单（只建单，不切网络、不支付），确认收银台页与 `/pay/checkout-counter-resp` 正常。

## 红线
- **绝不读取、打印、复制任何 api_key / api_secret / passphrase 的值**（DB 查询只取 status/remark/长度）。**不要动数据库里的任何凭证与开关**——只改前端代码与后端常量，绝不碰 settings/chains/wallet 表。
- 不动金额、汇率逻辑；不动 USDT0→USDT 的显示、不动免 Gas 角标、不动中间页直跳；不动 TokenPay；不动 `/settings/xlayer` 路由；不动 return_url 的自动推导逻辑。
- 最后写一份完成报告：每一点改了哪里（文件+做法）、构建与 curl 验证结果、新二进制 sha256、测试单号。
