# zcode 任务：OKX OnchainOS 配置页五处整改（用户反馈）

背景：本轮刚把 X Layer 融入管理后台（设计文档 `XLAYER-ADMIN-INTEGRATION-DESIGN.md`，已部署，二进制已上线）。用户看了“OKX OnchainOS 配置”页（路由 `/settings/xlayer`，chunk `xlayer-7Kx2Qw9Z.js`），提出 5 点意见，全部改掉。**模型：默认 GLM-5.3-Flash，不要切旗舰。**

## 1. 开通引导卡片样式太丑 → 重排版
- 现状：引导卡是新脚本注入的纯文本编号列表（`list-decimal` 不在已编译 CSS 里所以改用了文本编号），视觉上像一段裸文本，没有卡片感。
- 要求：把它做成一个真正像样的卡片。先读该页 chunk 里已有的卡片/表单样式（以及 OkPay 配置页 chunk `okpay-DzwNhqlx.js` 的排版），**只用已编译进 CSS 的 Tailwind 类**（用之前先在构建产物 CSS 里确认类名存在，不存在的类不会生效）。目标效果：有明确的卡片容器（边框/底色/内边距）、清晰的标题、步骤之间有呼吸感、dev-portal 链接做成显眼的可点击按钮/链接、50125 说明做成高亮提示（比如警告色小块）。不要为了好看引入新 CSS 文件或改动全局样式。
- 内容先按第 4 点精简（见下），再排版。

## 2. Passphrase 不要跟 API Key、API Secret 放在一块
- 现状：表单里 Passphrase（密码）和 API Key、API Secret 在同一个凭证组里。
- 要求：把 Passphrase 单独拎出来，放到一个独立的分组/区块里（有自己的小标题和一句说明，例如“创建 API Key 时设置的 Passphrase”），与 API Key + API Secret 所在的组视觉上分开。只改布局与分组，不改字段名、保存逻辑和后端接口。

## 3. return_url（支付完成回跳地址）不要手动填，调接口时自动传
- 现状：本轮刚给该页补了 return_url 输入框；用户明确反对——支付完成后的回跳地址不应该由管理员手动填，**应该在后端调 OKX 接口建单时自动传过去**。
- 要求：
  1. 删掉该页的 return_url 输入框（schema/defaultValues/load/save/reset 里同步清理，不要留死字段）。
  2. 读 `src/model/service/xlayer_service.go` 里的 OKX 建单函数，看 returnUrl 现在怎么传（是不是读 settings 的 return_url 键）。改成**建单时自动推导**：优先用订单自带的 merchant `redirect_url`（商户下单时给的支付后回跳地址）；没有 redirect_url 时回退到该笔订单的收银台页 URL（`{公网基址}/cashier/{trade_id}`）。settings 里的 return_url 键不再作为输入来源（可保留作最后兜底，但页面上不再出现、不再要求填写）。
  3. 不要改变 OKX 建单的其他字段、金额与汇率逻辑。

## 4. 删掉引导里关于收款地址与代币开关的废话
- 现状：引导卡的步骤里有“去地址管理配 xlayer 地址、去链与代币开代币”这类内容。
- 要求：**把引导卡里所有讲收款地址和代币开关的步骤/段落整个删掉**（地址和代币各自有专门的管理页面，不需要在引导里重复讲）。引导只保留真正需要讲的：去 dev-portal 建应用 → 开通 Onchain OS Payment 商家权限 → 生成 Key+Secret+Passphrase → 回填本页保存 → 小额测试单验证到 settled，以及 50125/50111 的含义说明。

## 5. 不要叫 USDT0，叫 USDT
- 现状：链与代币模块（`/chains`，chunk `chains-tUDg5ohy.js`）里 X Layer 的代币列表显示为 `USDT0`；收银台早已只显示 USDT。
- 要求：管理后台里所有**显示** USDT0 代币的地方，一律显示为 `USDT`（与收银台一致）。**只改显示**：数据库 `chain_tokens` 的 symbol、settings 的 allow_tokens、switch-network 校验、OKX 接口传参里一律继续用 `USDT0`（OKX 要的就是 USDT0，改标识会断支付）。做法参考收银台已有的做法：在渲染层做 USDT0→USDT 的显示映射（标签与图标符号都映射），不要动任何标识与逻辑。

## 构建与部署（必须走标准顺序）
重建顺序：`git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → `build-xlayer-admin-integration.py` → `rename-xlayer-chunks.py` → `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`（`src/` 目录下；若报 `go: not found`，把 `~/go/bin` 加进 PATH 再跑）。改过的 JS 全部 `node --check`。go 受影响包测试 + `go vet`（已知既有失败 `TestProcessAptosLedgerRoundMarksMatchingUSDTOrderPaid` 与本次无关，干净 HEAD 同样失败，不用修）。
部署：`instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`（沙箱内 sha256 核对）→ `docker cp /data/home/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`（instabox 命令在 `~/workspace/bin/instabox`）。重启前确认沙箱 `epusdt` 是 Running。
curl 验证：`/` 与 `/sign-in` 200；`/payments/gmpay/v1/config` 200 且 `xlayer.allow_tokens` 仍为 `["USDC","USDT0","USDG"]`；改过的 chunk 全部 200。`~/xlayer-create-only.py 10` 建一笔 ¥10 测试单（只建单，不切网络、不支付），确认收银台页与 `/pay/checkout-counter-resp` 正常。

## 红线
- **绝不读取、打印、复制任何 api_key / api_secret / passphrase 的值**（DB 查询只取 status/remark/长度）。
- 不动金额、汇率逻辑；不动 USDT0→USDT 的收银台显示、不动免 Gas 角标、不动中间页直跳；不动 TokenPay；不动 `/settings/xlayer` 路由。
- 不要为了改样式引入新 CSS 或改全局样式；不要把 USDT0 的标识改成 USDT。
- 最后写一份完成报告：每一点改了哪里（文件+做法）、构建与 curl 验证结果、新二进制 sha256、测试单号；并说明 return_url 自动推导的最终取值逻辑。
