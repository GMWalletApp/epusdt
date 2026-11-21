# zcode 任务：OKX OnchainOS 配置页第三轮整改——三个凭证字段合进一个框

背景：前两轮把 Passphrase 从 API Key、API Secret 里拆出来单独成组，但用户明确说了：**三个要放在一个框里**。之前理解反了，本轮改回来：API Key、API Secret、Passphrase 三个字段放进**同一个**边框卡片里。**模型：默认 GLM-5.3-Flash，不要切旗舰。**

## 要求
- 文件：`build-xlayer-admin-integration.py` 的表单中部（`FORM_MIDDLE`）。
- 做法：只留一个凭证卡片，标题用「API 凭证」（或同类能涵盖三样的标题），卡片内含 API Key、API Secret、Passphrase 三个字段。Key 与 Secret 可以并排一行，Passphrase 单独一行在下面（或三者纵向排列，以页面实际效果为准，关键是三者同属一个卡片、没有中间分隔线把它们隔开）。
- Passphrase 的说明文字保留（创建 API Key 时自己设置的密码短语，不是 OKX 生成的；每次签名请求都会随 Key、Secret 一同校验，丢失后只能删除 Key 重建），放在 Passphrase 字段下方或卡片内合适位置。
- 删掉上一轮加的 Passphrase 独立区块和中间那条 `border-t` 分隔线。接口超时、API 地址那两格保持不动；引导卡（5 步 + dev-portal 按钮、无错误码）保持不动。
- 字段名、zod schema、load/save/reset 载荷、后端接口全部未动。

## 构建与部署（必须走标准顺序）
重建顺序：`git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → `build-xlayer-admin-integration.py` → `rename-xlayer-chunks.py` → `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`（`src/` 目录下；若报 `go: not found`，把 `~/go/bin` 加进 PATH 再跑）。改过的 JS 全部 `node --check`。go 受影响包测试 + `go vet`。
部署：`instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`（沙箱内 sha256 核对）→ `docker cp /data/home/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`（instabox 命令在 `~/workspace/bin/instabox`）。重启前确认沙箱 `epusdt` 是 Running。
curl 验证：`/` 与 `/sign-in` 200；`/payments/gmpay/v1/config` 200 且 `xlayer.allow_tokens` 仍为 `["USDC","USDT0","USDG"]`、`xlayer.enabled` 为 true；改动 chunk 全部 200；线上 xlayer chunk 里搜不到 `50125`/`50111`/`常见报错`。`~/xlayer-create-only.py 10` 建一笔 ¥10 测试单（只建单，不切网络、不支付），确认收银台页与 `/pay/checkout-counter-resp` 正常。

## 红线
- **绝不读取、打印、复制任何 api_key / api_secret / passphrase 的值**（DB 查询只取 status/remark/长度）。**不要动数据库里的任何凭证与开关**——只改前端代码与构建，绝不碰 settings/chains/wallet 表。
- 不动金额、汇率逻辑；不动 USDT0→USDT 的显示、不动免 Gas 角标、不动中间页直跳；不动 TokenPay；不动 `/settings/xlayer` 路由；不动 return_url 的自动推导逻辑。
- 最后写一份完成报告：改了哪里（文件+做法）、构建与 curl 验证结果、新二进制 sha256、测试单号。
