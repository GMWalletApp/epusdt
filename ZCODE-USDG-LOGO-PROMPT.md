# zcode 任务：给收银台 X Layer 代币下拉加 USDG 真实 logo

## 背景
epusdt 收银台（X Layer 支付）代币下拉里，USDC 和 USDT 都有 logo，USDG 只有文字、没有图标。
原因已查清：`src/www/assets/crypto-icon-yn0aBrU6.js` 里的图标映射表 `u`（symbol → {network, contractAddress}）没有 `USDG` 条目，
图标组件查不到就回落成灰色圆圈（深色主题下几乎看不见）。上次 USDT0 的修复只加了 USDT0 条目，没动 USDG。

## 已验证的事实（不用再查，直接用）
- 图标解析规则：`blockchains/${network}/assets/${contractAddress}/logo.png`，仓库是 `GMWalletApp/assets`、分支 `main`，
  经 jsdelivr 等 CDN 镜像加载。
- USDG 的 logo 在该仓库里存在：`blockchains/ethereum/assets/0xe343167631d89B6Ffc58B88d6b7fB0228795491D/logo.png`
  （另有 info.json）。该地址是 Paxos USDG 在以太坊的官方合约地址（X Layer 上的 USDG 合约是
  `0x4ae46a509f6b1d9056937ba4500cb143933d2dc8`，不要混淆——图标映射用以太坊那个地址，跟 USDT/USDC 的做法一样）。
- 现有映射格式参考：`USDC:{network:'ethereum',contractAddress:'0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48'}`、
  `USDT:{network:'ethereum',contractAddress:'0xdAC17F958D2ee523a2206206994597C13D831ec7'}`、
  `USDT0:{network:'ethereum',contractAddress:'0xdAC17F958D2ee523a2206206994597C13D831ec7'}`（上次补丁加的）。

## 任务
1. 先读仓库根目录的 `build-xlayer-usdt-display.py`（上次 USDT0 修复的可复现补丁脚本），按同样套路扩展它：
   在 crypto-icon 的 `u` 表里增加 `USDG:{network:'ethereum',contractAddress:'0xe343167631d89B6Ffc58B88d6b7fB0228795491D'}`，
   放在 USDT0 条目后面。不要只手工改 built 文件——流水线开头会 `git checkout -- src/www/assets` 还原，
   所以必须改在补丁脚本里才可复现。
2. 按既定重建顺序完整走一遍：
   `git checkout -- src/www/assets` → `build-xlayer-ui.py` → `build-xlayer-badge.py` →
   `build-xlayer-redirect.py` → `build-xlayer-usdt-display.py` → `rename-xlayer-chunks.py` →
   go 构建（在 `src/` 下 `BUILD_TAG=0.0.0-xlayer-dev CGO_ENABLED=0 make linux-amd64`，产物
   `src/bin/epusdt-linux-amd64`，必须是静态链接；go 在 `~/go/bin/go`）。
   每一步用 `node --check` 之类方式校验改过的 JS 语法没坏（上次曾出现过转义导致的 SyntaxError）。
3. 部署到 InstaBox 沙箱 `epusdt`（instabox 命令用 `~/workspace/bin/instabox`）：
   `instabox cp src/bin/epusdt-linux-amd64 epusdt:epusdt-new`，进沙箱核对 sha256，
   再 `docker cp ~/epusdt-new epusdt-epusdt-1:/app/epusdt && docker restart epusdt-epusdt-1`。
   沙箱公网地址：`https://8000-agqzyqhqzzuvjltnmbmwhrarti.instabox.tech`。
4. 用 curl 验证线上生效：已服务的 crypto-icon 文件里含有 USDG 条目；
   `GET /payments/gmpay/v1/config` 的 allow_tokens 仍是 `USDC,USDT0,USDG`；
   有条件的话 curl 一下 CDN 上的 USDG logo.png 是否能拿到（VM 出站走代理，若拿不到就如实写进报告，等浏览器验证）。

## 红线
- 只改显示：`USDT0` 在 allow_tokens、switch-network 入参、OKX 建单请求、数据库里一律保持原样；不要改任何后端逻辑。
- 不要动 USDC/USDT/USDT0 已有的条目；不要碰 TokenPay（那是另一个仓库和沙箱）。
- 不要写任何真实凭证到文件里。

## 汇报
完成后报告：新二进制的 sha256、curl 验证结果（crypto-icon 含 USDG、config 不变）、以及一句可供浏览器验证的收银台检查方法。
如果某一步失败，写清楚失败在哪一步、报错原文，不要猜。
