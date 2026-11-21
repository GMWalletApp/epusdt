# zcode 任务：OKX OnchainOS 配置页第二轮整改（用户反馈）

背景：上一轮已把“OKX OnchainOS 配置”页（路由 `/settings/xlayer`，chunk `xlayer-7Kx2Qw9Z.js`）改成引导卡 + Key/Secret 一组、Passphrase 单独一组，并部署上线。但用户在页面上反馈两点，本轮改掉。**模型：默认 GLM-5.3-Flash，不要切旗舰。**

## 1. 引导卡里的错误码整段去掉
- 现状：引导卡底部有一块琥珀色警告块，讲 50125（Key 无商家支付权限）、50111（Key 错误）等错误码含义。
- 要求：**把这块错误码说明整个删掉**（引导卡只留 5 个步骤 + dev-portal 按钮）。不要把它移到页面其他位置，也不要留一句“错误码见文档”之类的话。删除后检查引导卡里不再出现 `50125`、`50111`、`61009` 字样。

## 2. Passphrase 必须跟 API Key、API Secret 视觉上彻底分开
- 现状：上一轮已把 Passphrase 放进一张单独的边框卡片（chunk 里能搜到 `Passphrase（密码）` 小标题），但用户在页面上看，三样还是一起的。
- 要求：先定位原因再动手——可能原因有两个：① 两张卡片嵌在同一个大表单分组/同一张外层卡片里，视觉上仍是一组；② 用户页面是旧版未刷新（chunk 文件名没变）。不管是哪种，**把分隔做得一眼就能看出来**：Passphrase 自成一个独立区块（自己的区块标题、与 Key/Secret 卡片之间有明确的垂直间距/分隔，不要共用同一个外层容器）；API Key 与 API Secret 可以仍在一组。只改布局与分组，不改字段名、保存逻辑和后端接口。
- 自查：改完后在构建产物里确认三样字段确实落在不同的顶层区块容器里，不要只凭“有个小标题”就当分开了。

## 构建与部署（必须走标准顺序）
重建顺序：`git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → `build-xlayer-admin-integration.py` → `rename-xlayer-chunks.py` → `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`（`src/` 目录下；若报 `go: not found`，把 `~/go/bin` 加进 PATH 再跑）。改过的 JS 全部 `node --check`。go 受影响包测试 + `go vet`（已知既有失败 `TestProcessAptosLedgerRoundMarksMatchingUSDTOrderPaid` 与本次无关，不用修）。
部署：`instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`（沙箱内 sha256 核对）→ `docker cp /data/home/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`（instabox 命令在 `~/workspace/bin/instabox`）。重启前确认沙箱 `epusdt` 是 Running。
curl 验证：`/` 与 `/sign-in` 200；`/payments/gmpay/v1/config` 200 且 `xlayer.allow_tokens` 仍为 `["USDC","USDT0","USDG"]`、`xlayer.enabled` 为 true；改动 chunk 全部 200；线上 xlayer chunk 里搜不到 `50125`/`50111`。`~/xlayer-create-only.py 10` 建一笔 ¥10 测试单（只建单，不切网络、不支付），确认收银台页与 `/pay/checkout-counter-resp` 正常。

## 红线
- **绝不读取、打印、复制任何 api_key / api_secret / passphrase 的值**（DB 查询只取 status/remark/长度）。注意：本次任务**不要动数据库里的任何凭证与开关**——上一轮误重置后凭证刚恢复好，只改前端代码与构建，绝不碰 settings/chains/wallet 表。
- 不动金额、汇率逻辑；不动 USDT0→USDT 的显示、不动免 Gas 角标、不动中间页直跳；不动 TokenPay；不动 `/settings/xlayer` 路由；不动 return_url 的自动推导逻辑。
- 最后写一份完成报告：每一点改了哪里（文件+做法）、构建与 curl 验证结果、新二进制 sha256、测试单号。
