# zcode 任务：把 OKX Payment（X Layer / Onchain OS Payment）融入管理后台已有配置流程

## 背景
epusdt 收款网关已接通 OKX Onchain OS Payment（X Layer），真实收款已跑通。
现在 X Layer 的配置是独立的设置页（`/settings/xlayer`），用户要求把它融入管理后台已有的配置流程，
而不是挂一个孤立页面。

## 现状（先读代码核实，不要凭印象）
- 仓库：当前目录（epusdt-source）。后端 Go 在 `src/`；管理后台是**只有编译产物的 SPA**（`src/www/assets/`），
  没有前端源码。已有做法是把成品 chunk 复制改名再注册路由：X Layer 独立设置页就是把 OkPay 设置页 chunk
  克隆成 `xlayer-7Kx2Qw9Z.js`、注册 `/settings/xlayer` 路由、在侧边栏和命令面板加入口（脚本 `build-xlayer-ui.py`）。
  先读 `build-xlayer-ui.py`、`build-xlayer-badge.py`、`build-xlayer-redirect.py`、`build-xlayer-usdt-display.py`、
  `rename-xlayer-chunks.py`，学会这套“补丁脚本 + 重建”的工作方式，再动手。
- X Layer 当前配置存在 settings 表 `xlayer` 组：enabled、api_key、api_secret、passphrase、
  api_url（默认 https://web3.okx.com，仅展示）、to_address、return_url、timeout_seconds（10）、
  allow_tokens（USDC,USDT0,USDG）。真实凭证已由用户在后台配好并启用。
- 后端关键文件：`src/model/service/xlayer_service.go`（签名建单/查询、HMAC-SHA256）、
  `src/model/service/order_service.go`（switchToXLayer）、`src/model/service/pay_service.go`、
  `src/task/listen_xlayer_job.go`（每 10 秒轮询，只有 settled 算已付）、
  `src/controller/comm/supported_asset_controller.go`（收银台 supported_assets）。
- 收银台显示层：`USDT0` 只在展示时显示为 `USDT`（labels 与 `Ei`），后端、配置、OKX 请求、数据库里一律保持 `USDT0`。
  任何改动都不得破坏这一点。
- 标准重建顺序（改完前端必须完整走一遍）：`git checkout -- src/www/assets` → `build-xlayer-ui.py` →
  `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` →
  `rename-xlayer-chunks.py` → go 构建（在 `src/` 下 `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`，
  产物 `src/bin/epusdt-linux-amd64`，必须是静态链接；go 在 `~/go/bin/go`）。
  改过的 JS 每个都要 `node --check` 校验（之前出过转义导致的 SyntaxError）。
- 部署：instabox 命令用 `~/workspace/bin/instabox`，沙箱名 `epusdt`：
  `instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`，进沙箱核对 sha256，
  再 `docker cp /data/home/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`
  （注意：`docker cp ~/epusdt-new` 里的 `~` 会被本地展开而失败，必须用沙箱绝对路径 `/data/home/epusdt-new`）。
  公网地址 `https://8000-agqzyqhqzzuvjltnmbmwhrarti.instabox.tech`，后台登录入口 `/sign-in`
  （该版本没有 `/admin` 地址）。

## 任务：分三步做

### 第一步：调研 + 写设计文档
先弄清管理后台已有三处流程在代码里是怎么实现的，再写设计文档到
`XLAYER-ADMIN-INTEGRATION-DESIGN.md`（仓库根目录），写清每个需求落到哪些表、哪些接口、哪些 chunk，
以及关键取舍。不要跳过这一步直接改代码。
- 地址管理：地址存在哪张表、什么结构？网络字段有哪些取值？新增/编辑地址走哪个接口？
- 链与代币模块：链和代币存在哪张表？开关状态怎么存、收银台怎么读到？
- 系统配置页：现有 “OkPay 配置” 是哪个 chunk/路由、配置存在哪个 settings 组？
- 现在的 `/settings/xlayer` 独立页与上述三处的关系：设计里决定它是保留、迁移还是并存，并说明理由。

### 第二步：按设计实现（用户要求的 4 点）
1. **地址管理可选择 X Layer**：地址的网络选择里增加 X Layer；只有启用后该地址才生效；
   X Layer 的收款地址（to_address）以这里的配置为准（设计里定好唯一来源，不要两处各存一份打架）。
2. **链与代币模块新增 X Layer**：加一条 X Layer 链，旗下 3 种稳定币（USDC、USDT0、USDG）各自可开关；
   开关结果要真实控制收银台的 X Layer 可选代币（与现有的 allow_tokens 打通，不要另起一套假开关）。
3. **系统配置里新增 “OKX OnchainOS 配置”**：其他相关配置项（API Key、API Secret、Passphrase、API 地址、
   超时、回调地址等）放在系统配置页，与 “OkPay 配置” 并列，标题就叫 “OKX OnchainOS 配置”。
4. **开通引导**：当用户要开启 X Layer 相关操作时，页面内引导用户去
   `https://web3.okx.com/onchainos/dev-portal` 创建并生成 API Key 等信息，并配上补充引导操作
   （中文步骤：去 dev-portal 建应用/开通 Onchain OS Payment 商家权限、生成 API Key + Secret + Passphrase、
   回填到本页、保存后验证；并说明常见报错含义，例如 OKX 报 50125 就是这把 Key 没有商家支付权限、
   需要去 dev-portal 开通——不要只贴一个链接就完事）。

### 第三步：构建、部署、验证
- 按标准重建顺序完整构建并部署到 `epusdt` 沙箱。
- 用 curl 验证：根目录 200、`/sign-in` 200、`/payments/gmpay/v1/config` 里 xlayer 仍为
  `["USDC","USDT0","USDG"]`、改过的 chunk 都能 200 拿到。
- 沙箱内用 `~/xlayer-create-only.py` 建一笔新的 ¥10 测试单（只建单、不支付、不切网络），
  确认建单接口正常、收银台能打开；不要真的支付。

## 红线
- 不要动真实凭证：不要读、不要打印、不要复制 api_key / api_secret / passphrase 的值，
  不要把它们写进任何文件、日志或设计文档。只验证“已配置/未配置”这种状态。
- 不要破坏现有收款链路：X Layer 支付、收银台、轮询、回调、USDT0 显示为 USDT、免 Gas 角标、
  中间页直跳，改完必须都还在。
- 现有的 `/settings/xlayer` 独立页：没有把握就保留它正常工作，不要删；设计里写清去留。
- 不要碰 TokenPay（那是另一个仓库和沙箱）。
- 不要改后端收款金额逻辑（汇率、tokenAmount 计算）。

## 汇报
完成后报告：设计文档路径、每个需求的实现位置（表/接口/chunk）、新二进制 sha256、
curl 验证结果、以及一句可供浏览器验证管理后台的方法（进哪个页面、点哪里、能看到什么）。
如果某一步失败，写清楚失败在哪一步、报错原文，不要猜。
