# Bitcoin 主网节点

三个服务使用同一个网络键 `bitcoin`。下表列出 2026-11-23 实测能用于扫块或查询地址余额的公开端点。

| 服务商 | 主网地址 | 适用服务 | 已验证的能力 |
| --- | --- | --- | --- |
| PublicNode | `https://bitcoin-rpc.publicnode.com` | ChainScanner、Broadcaster 广播候选 | Bitcoin Core RPC。主网创世块、高度、历史块 170 的全部交易及输入前序输出，且 txindex 已同步。[服务入口](https://bitcoin.publicnode.com/) |
| NOWNodes | `https://public-btc.nownodes.io` | ChainScanner | Bitcoin Core RPC。主网创世块、历史块 170 的全部交易及输入前序输出。[服务入口](https://nownodes.io/nodes/bitcoin-btc) |
| Blockstream | `https://blockstream.info/api` | BalanceKeeper | Esplora 地址索引。主网创世块和公开地址的已确认收支统计。[接口文档](https://github.com/Blockstream/esplora/blob/master/API.md) |
| mempool.space | `https://mempool.space/api` | BalanceKeeper | Esplora 兼容地址索引。主网创世块和公开地址的已确认收支统计。[接口文档](https://mempool.space/docs/api/rest) |

Go 适配器还通过两家 Core 节点分别读取了主网高度 977080，两次均解析出 3,246 笔交易。两个地址索引在高度 977082 对同一公开地址返回了相同的已确认余额。

## 配置

- **ChainScanner** 在 `networks` 数组中加入 `{"name":"bitcoin","protocol":"jsonrpc","url":"https://bitcoin-rpc.publicnode.com","confirmations":2}`。扫描逐块读取 `getblock(hash,3)`，从 `vin[].prevout.scriptPubKey` 取得支付方，从每个输出的锁定脚本取得收款和找零地址。普通输入缺少前序输出时重试该块。自建节点需能提供待扫区间的区块和 undo 数据，按哈希查询历史交易还需同步 txindex。查询结果的 `hex` 是完整的序列化交易，`txid` 经本地解码复算，未入块交易按 `pending` 处理，确认后返回 `blockhash`。[Bitcoin Core 说明](https://bitcoincore.org/en/releases/23.0/)
- **BalanceKeeper** 在 `networks` 对象中加入 `"bitcoin":{"rpc":"https://blockstream.info/api"}`。登记 BTC 使用 `{"name":"BTC","contract_address":null}`，按资产查询使用 `native`，最小单位是聪。自建可使用兼容 Esplora 的地址索引。[配置示例](https://github.com/GMWalletApp/BalanceKeeper/blob/main/config.example.json)
- **Broadcaster** 使用 `POST /networks/bitcoin/transactions` 接收已签名交易。启用提供商需要按项目的现有准入流程验证写权限与 txindex。本次公共节点测试仅执行只读请求，实际广播链路使用本地签名交易样例验证。[提供商准入与 BTC 接口](https://github.com/GMWalletApp/Broadcaster/blob/main/docs/api.zh-CN.md)
