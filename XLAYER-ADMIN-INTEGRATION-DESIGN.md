# X Layer（OKX OnchainOS Payment）融入管理后台已有配置流程 — 设计文档

日期：2026-11-20。前置文档：`XLAYER-EXPLORATION.md`、`XLAYER-IMPLEMENTATION.md`。
本文回答任务要求的三处调研问题，并给出 4 个需求的落地方案（表 / 接口 / chunk / 取舍）。

---

## 一、现状调研（已核实代码，非凭印象）

### 1. 地址管理（/addresses）

- **表**：`wallet_address`（`src/model/mdb/wallet_address_mdb.go`）。
  列：`network`+`address`（联合唯一索引）、`status`（**1=启用 2=禁用**）、`remark`、`source`（manual/import）、软删除。
  网络“白名单”常量在同一文件：tron/solana/ethereum/binance/polygon/plasma/ton/aptos —— **没有 xlayer**，但 `network` 是普通字符串列，任何小写字符串都能存。
- **接口**（`src/controller/admin/wallet_controller.go` + `src/route/router.go:312-318`）：
  `GET/POST /admin/api/v1/wallets`、`GET/PATCH /wallets/{id}`、`POST /wallets/{id}/status`、`DELETE /wallets/{id}`、`POST /wallets/batch-import`。新增走 `POST /wallets`（`AdminAddWalletRequest{network,address,remark}`）。
- **前端**：`addresses-JEZCoidP.js`。关键事实：**新增/批量导入弹窗里的“网络”下拉不是硬编码，而是来自 chains-store（`GET /admin/api/v1/chains`）**：
  `let{chains}=U(); f=chains.map(e=>e.network).filter(...)`。列表页的网络筛选 chip 来自已有钱包行的 network。
- **生效逻辑**：`data.GetAvailableWalletAddress()`（status=1）→ `buildSupportedAssets()` 里“某网络有启用地址才有收银台入口”（`networkHasWallet`）。
  即：**地址禁用 = 该网络从收银台消失**。这条现有规则正好满足“只有启用后该地址才生效”。

### 2. 链与代币模块（/chains）

- **表**：
  - `chains`（`mdb/chain.go`）：`network`（唯一）、`display_name`、`enabled`、`min_confirmations`、`scan_interval_sec`、`extra`。
  - `chain_tokens`（`mdb/chain_token.go`）：`network`+`symbol`（联合唯一）、`contract_address`、`decimals`、`enabled`、`min_amount`。
- **seeds**（`model/dao/mdb_table_init.go`）：`seedChains()` 8 条、`seedChainTokens()` 按链若干条，全部 `ON CONFLICT DO NOTHING`（幂等，管理员改动可持久）。
- **前端**：`chains-tUDg5ohy.js`。左栏链列表 + 每链开关（`PATCH /admin/api/v1/chains/{network}`），右栏 token 表 + 开关（`POST /admin/api/v1/chain-tokens/{id}/status`），新增 token 对话框的网络下拉同样来自 chains 接口。（token 的“编辑/删除”菜单是 coming-soon 占位，只有新增与开关可用。）
- **收银台读取**：`controller/comm/supported_asset_controller.go buildSupportedAssets()` =
  `enabled chains` ∩ `有启用钱包的网络` ∩ `enabled chain_tokens`（且 `service.ChainTokenReadyForPayment`：未知网络要求 contract_address 非空）。`/payment-setup` 页展示同一份 `supported_assets`。
- **扫描器安全性**（已核实）：所有扫描任务按网络硬编码（listen_eth/bsc/polygon/plasma/sol/ton/aptos + evm_backfill 均以固定 network 调 `IsChainEnabled`），**没有任何组件按 chains 行动态起扫描器**。`chains` 多一行 xlayer 不会引发任何链上扫描行为。

### 3. 系统配置页（OkPay 配置在哪）

- 用户所说“系统配置页”= 管理后台 **/settings 区域**（布局标题 i18n 就是“系统配置”，`messages-Cl9iE86Z.js` `Us=()=>系统配置`）。左栏导航（`route-BkjShyH8.js`）：/settings、telegram、system、pay（汇率）、epay、**okpay**、xlayer。
- **OkPay 配置**：chunk `okpay-DzwNhqlx.js`，路由 `/settings/okpay`，读写 settings 表 **group=`okpay`**（`okpay.enabled/shop_id/shop_token/api_url/callback_url/return_url/timeout_seconds/allow_tokens`）。接口：`GET /admin/api/v1/settings?group=` + `PUT /admin/api/v1/settings`（批量 upsert）+ `DELETE /settings/{key}`；settings 读取有进程内缓存（`data/settings_data.go`）。
- `/settings/system`（`system-CkRcAjXh.js`）只有“日志级别”一节，**不含** OkPay —— “与 OkPay 配置并列”只能指 /settings 导航里的平级页面。

### 4. 现有 /settings/xlayer 独立页与三处的关系

- chunk `xlayer-7Kx2Qw9Z.js`（`build-xlayer-ui.py` 从 okpay chunk 克隆）、路由 `/settings/xlayer`（router 注册 + mapDeps[141]）、侧边栏/命令面板入口标题为字面量 `"X Layer"`。
- 表单字段：enabled / api_key / api_secret / passphrase / api_url(只读) / **to_address** / **allow_tokens** / timeout_seconds / return_url（**return_url 无输入框，只随 payload 提交**）。全部写 settings 组 `xlayer`。
- 问题：to_address、allow_tokens 与地址管理、链与代币模块**职责重叠**；页面是孤立品牌（"X Layer"），未融入“系统配置”语境；无任何开通引导。

**去留决定：保留并升级（迁移式保留），不删除。**
理由：路由与 chunk 已稳定工作（红线“没有把握就保留”）；它本来就挂在 /settings 导航里、结构上与 OkPay 配置同构，缺的不是位置而是“身份”和“分工”。改造为：标题改为“OKX OnchainOS 配置”、与 OkPay 配置真正并列；收走 to_address（→地址管理）与 allow_tokens（→链与代币），补 return_url 输入框，加开通引导。删除该页反而要动 router mapDeps，风险更高收益为零。

---

## 二、需求落地方案

### 需求 1：地址管理可选择 X Layer；启用后才生效；to_address 唯一来源

- **chains 表加 `xlayer` 行后，地址页网络下拉自动出现 X Layer**（前端零改动，chains-store 驱动）。地址按原样大小写保存（**不把 xlayer 加进 `isEVMNetwork`**，避免迁移时改变用户已配好的收款地址大小写；结算校验用 `EqualFold`，不受影响）。
- **唯一来源 = `wallet_address` 表**：
  - 启动迁移 `seedXLayerWalletFromSettings()`（`mdb_table_init.go`）：若 settings `xlayer.to_address` 非空且 xlayer 无任何（含软删）钱包行 → 落一行 `{network:xlayer, address:<值>, status:1, source:manual, remark:"迁移自 OKX OnchainOS 配置"}`；有软删行则恢复。幂等。
  - `data.GetXLayerToAddress()` 改为：取 `wallet_address` 中 network=xlayer、status=1、**id 最小的一条**（X Layer 托管收款只有一个 `to`，多条启用地址时取最早创建的，确定性行为）；没有任何启用行时回落旧 settings 键（兼容未重启完迁移的极端场景 + 现有单测）。设计上 settings 键降级为 legacy 兜底，不再是编辑入口。
  - 建单（`switchToXLayer`/`completeWaitSelectXLayerOrder`）与结算校验（`PollXLayerProviderOrders` → `xlayerSettleMatches`）都走这一个 getter —— 天然一致。
- “只有启用后才生效”由现有机制保证：禁用地址 → `GetAvailableWalletAddress` 不含 xlayer → 收银台入口消失、建单报配置不全。
- 不新增接口：`POST /wallets`（network=xlayer）即新增入口。

### 需求 2：链与代币模块新增 X Layer + 3 个稳定币真开关

- **seeds**：`seedChains()` 加 `{Network:"xlayer", DisplayName:"X Layer", Enabled:true}`；`seedChainTokens()` 加 3 行（合约地址取自 `xlayer_service.go` 现有 map）：
  USDC `0xb6ce...3061`、USDT0 `0x779d...7736`、USDG `0x4ae4...2dc8`，decimals=6，enabled=true。
- 前端零改动：chains 页自动出现 X Layer 链 + 3 个 token 开关。
- **真源切换**：`data.GetXLayerAllowTokens()` 改为**读 `chain_tokens`（network=xlayer）的 enabled 行**；若表中不存在任何 xlayer token 行（例如测试库未跑 seed），才回落 settings `xlayer.allow_tokens`。全禁 → 返回空 → 收银台无代币、switch 拒绝。settings 键降级为 legacy 兜底。
- **收银台读取改造**（`buildSupportedAssets()`）：**删除 xlayer 伪网络块**，让通用路径（chain enabled ∩ 有启用钱包 ∩ enabled chain_tokens）产生唯一一条 xlayer 条目；对 xlayer 附加两个条件：
  1. `data.GetXLayerEnabled()`（settings 总开关仍是 master switch，同时控制轮询任务与 switch）；
  2. symbol 过滤 `service.IsPayableXLayerToken()`（Go 合约 map 里存在的才展示，防止管理员加了个映射外符号导致建单必败）。
  这样 cashier、payment-setup、`GET /payments/gmpay/v1/config`（`xlayer.allow_tokens` 同样走新 getter）三处口径一致。
- **开关联动验证链**：token 开关（chain_tokens.enabled）→ `ListEnabledChainTokensByNetwork` → supported_assets 收银台列表；同一数据也约束 `xlayerTokenAllowed`（switch 校验，因为它读 GetXLayerAllowTokens）。**不是假开关。**
- **关键取舍 —— 之前“不加 chain 行”的旧决定被有意推翻**：`XLAYER-IMPLEMENTATION.md` 曾明确不加 chains 行，理由是“会出现永远无法确认的链上单”。本设计加行同时**在 `CreateTransaction` 增加守卫：`network=="xlayer"` 直接返回 `ChainNotEnabled`**（与加行前行为完全一致：X Layer 只能通过占位单 + switch-network 进入；直接带 network=xlayer 的建单依旧被拒）。守卫同时挡住 epay `type=usdc.xlayer` 选择器路径（最终同样落到 CreateTransaction 被拒，行为不变）。switch-network 的 xlayer 分支在最前面拦截（`order_service.go:594`），链上分支/`completeWaitSelectOrder` 永远收不到 xlayer，无需再改。

### 需求 3：系统配置新增“OKX OnchainOS 配置”

- **改造现有 `/settings/xlayer` 页**（保留路由，深链接不断）：
  - 侧边栏（route-BkjShyH8.js）与命令面板（search-provider-DPXFLA4q.js）标题 `"X Layer"` → `"OKX OnchainOS 配置"`，与 OkPay 配置平级并列。
  - 页头 title/description 改中文（“OKX OnchainOS 配置”/说明托管收银台）。
  - **移除** to_address、allow_tokens 两个输入（职责移交地址管理、链与代币；zod schema、defaultValues、load、save、reset payload 同步移除，reset 不再误清这两个键）。
  - **补上** return_url 输入框（schema/defaultValues 里本就有，只是没有输入框；X Layer 无 webhook，return_url 是支付完成后的浏览器回跳）。
  - 保留：enabled（总开关）、API Key、API Secret、Passphrase、API URL（只读 https://web3.okx.com）、Timeout (seconds)。
  - 保存按钮/toast 文案 → “保存 OKX OnchainOS 配置”/“OKX OnchainOS 配置已保存”。
- 后端接口零改动（settings 组 `xlayer` 的 PUT 通道原样复用）。

### 需求 4：开通引导

- 引导卡片放在“OKX OnchainOS 配置”页表单顶部（页面本身就是“X Layer 相关操作”发生地；常驻、紧凑）。
- 内容（中文，硬编码字符串，与该页现有做法一致 —— 无 i18n 键可用）：
  1. 打开 dev-portal 链接（`https://web3.okx.com/onchainos/dev-portal`，新窗口），登录 OKX 账号；
  2. 创建应用 / 为应用开通 **Onchain OS Payment 商家支付权限**；
  3. 生成 **API Key + Secret + Passphrase**（三样缺一不可）；
  4. 回填到本页，API 地址保持 https://web3.okx.com，保存；
  5. 收款地址去「地址管理」新增 network=xlayer 并启用；代币开关在「链与代币」的 X Layer 分组；
  6. 开启开关、保存后用小额测试单验证（只有轮询到 settled 才算支付成功）。
  另列常见报错：**50125 = 这把 Key 没有商家支付权限，需回 dev-portal 开通**（50111 才是 Key 错误）；“xlayer config incomplete”=必填项/收款地址缺失；61009=merchantPaymentId 重复；超时=API 地址不可达或 timeout 过小。
- 实现约束：只用已编译进 `index-*.css` 的类（已逐个核实：rounded-md、border、border-dashed、p-3、mt-2/mt-3、space-y-1、text-sm、text-xs、text-muted-foreground、font-medium、leading-relaxed、break-all、text-primary、underline-offset-4、hover:underline 均存在；`list-decimal` 不存在 → 用 "1." 文本编号）。链接复用 pay 页的 `<a>` 类组合。

---

## 三、改动清单

### 后端（Go）

| 文件 | 改动 |
|---|---|
| `src/model/mdb/wallet_address_mdb.go` | 加 `NetworkXLayer = "xlayer"` 常量 |
| `src/model/dao/mdb_table_init.go` | `seedChains()`+xlayer 行；`seedChainTokens()`+3 行；新增 `seedXLayerWalletFromSettings()`（含软删恢复） |
| `src/model/data/settings_data.go` | `GetXLayerToAddress()` → wallet 表优先；`GetXLayerAllowTokens()` → chain_tokens 优先 |
| `src/controller/comm/supported_asset_controller.go` | 删伪网络块；通用路径对 xlayer 加总开关+合约 map 过滤 |
| `src/model/service/xlayer_service.go` | 导出 `IsPayableXLayerToken()` |
| `src/model/service/order_service.go` | `CreateTransaction` 加 xlayer 守卫（ChainNotEnabled） |
| `src/controller/admin/settings_controller.go` | 注释更新：to_address/allow_tokens 标注为 legacy 兜底 |
| 测试 | 新增：to_address 真源优先级、supported_assets 由 chain_tokens/总开关/钱包驱动；既有测试靠 fallback 路径保持通过 |

### 前端（新补丁脚本 `build-xlayer-admin-integration.py`）

标准重建顺序变为：`git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` → `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → **`build-xlayer-admin-integration.py`（新）** → `rename-xlayer-chunks.py` → go 构建。
新脚本改 3 个文件，逐个 `node --check`：

| 文件 | 改动 |
|---|---|
| `xlayer-7Kx2Qw9Z.js` | 页头/开关/按钮/toast 中文化；删 toAddress+allowTokens（schema/default/load/save/reset 六处）；补 return_url 输入；插开通引导卡片 |
| `route-BkjShyH8.js` | 侧边栏标题 → “OKX OnchainOS 配置” |
| `search-provider-DPXFLA4q.js` | 命令面板标题 → 同上 |

### 明确不做

- 不动：金额/汇率逻辑（`xlayerAmounts`）、轮询节奏、USDT0→USDT 显示层映射、免 Gas 角标、中间页直跳补丁、TokenPay、真实凭证（任何文件/日志/文档都只写“已配置”状态，不写值）。
- 不把 xlayer 加进 `isEVMNetwork`（保持地址原样大小写）。
- 不删 `/settings/xlayer` 路由；不新建 chunk 文件（不碰 router mapDeps）。

---

## 四、风险与回滚

- **最大风险**：收银台 supported_assets 从“伪网络块”切到“通用路径”。缓解：迁移 seed 保证 chain 行+钱包行+3 token 全部就位后才出现在收银台（与改造前可见状态一致）；测试覆盖“缺任一条件即消失”。
- **回滚**：git revert 后端 + `git checkout -- src/www/assets` 重跑旧脚本即可；chains/chain_tokens/wallet 多出的行是数据不是 schema 变更，旧二进制读它们也无副作用（旧版 supported_assets 走伪网络块，忽略 chains 里的 xlayer；旧版 GetXLayerToAddress 只读 settings）。
