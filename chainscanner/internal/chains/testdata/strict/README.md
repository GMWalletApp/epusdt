# Sources for strict transaction fixtures

These offline responses were collected on 2026-11-23. JSON-RPC envelopes, list wrappers and whitespace were removed. Tests introduce provider differences in memory and exercise both voting orders.

| Fixture | Source and method | Scope |
| --- | --- | --- |
| evm | [Ethereum PublicNode](https://ethereum-rpc.publicnode.com), `eth_getTransactionByHash` and `eth_getTransactionReceipt` | Mined Ethereum transaction and four receipt logs |
| evm-second-receipt | [Flashbots Protect](https://rpc.flashbots.net), `eth_getTransactionReceipt` | Same transaction as evm, queried independently |
| solana | [Solana getTransaction example](https://solana.com/docs/rpc/http/gettransaction), queried on api.devnet.solana.com with JSON encoding | Confirmed system transfer with raw compiled instructions |
| tron, tron-native | [TronGrid transaction body](https://developers.tron.network/reference/gettransactionbyid) and [execution receipt](https://developers.tron.network/reference/gettransactioninfobyid), queried on api.trongrid.io | TRC-20 transfer with a log, and a native TRX transfer with bandwidth usage but no VM result |
| aptos, aptos-epilogue | [Aptos mainnet REST API](https://fullnode.mainnet.aptoslabs.com/v1) | User transaction excerpt with fungible-asset events and resource writes. The block epilogue response has no event list. |
| sui | [Mysten Labs gRPC API](https://github.com/MystenLabs/sui-apis/blob/main/proto/sui/rpc/v2/executed_transaction.proto), GetTransaction on fullnode.mainnet.sui.io:443 | Finalized checkpoint, sender, execution status and balance changes requested with a read mask |
| ton | [TON Center v3](https://toncenter.com/api/v3/index.html), transactions on workchain 0 | Indexed incoming message with raw contents and optional decoded metadata |

Each fixture includes the public transaction ID. The TON LiteServer path independently checks block inclusion proofs. The indexed TON fixture exercises the separate two-index fallback.
