package scanner

// NodeEndpoint identifies one provider interface on one ledger.
type NodeEndpoint struct {
	Network  string `json:"network"`
	Protocol string `json:"protocol"`
	URL      string `json:"url"`
}

var builtinNodes = []NodeEndpoint{
	{"tron", "http", "https://tron-mainnet.token.im"},
	{"tron", "http", "https://api.trongrid.io"},
	{"tron", "grpc", "grpc://18.206.50.220:50051"},
	{"tron", "grpc", "grpc://44.205.88.94:50051"},
	{"tron", "grpc", "grpc://13.124.62.58:50051"},
	{"tron", "grpc", "grpc://43.198.147.230:50051"},
	{"tron", "grpc", "grpc://16.162.38.213:50051"},
	{"ton", "jsonrpc", "https://ton-mainnet.token.im/api/v2/jsonRPC"},
	{"ton", "jsonrpc", "https://toncenter.com/api/v2/jsonRPC"},
	{"ethereum", "http", "https://eth-mainnet.token.im"},
	{"ethereum", "http", "https://web3.mybitkeep.vip"},
	{"ethereum", "http", "https://geth.mybitkeep.vip"},
	{"ethereum", "http", "https://web3os.bitkeep.pro"},
	{"ethereum", "http", "https://eth626892d.jccdex.cn"},
	{"base", "ws", "wss://base-rpc.publicnode.com"},
	{"base", "http", "https://base-mainnet.token.im"},
	{"base", "http", "https://mainnet.base.org"},
	{"base", "http", "https://1rpc.io/base"},
	{"base", "http", "https://base.llamarpc.com"},
	{"base", "http", "https://base-mainnet.gateway.tatum.io"},
	{"arbitrum", "http", "https://arb1.arbitrum.io/rpc"},
	{"polygon", "http", "https://polygon-mainnet.token.im"},
	{"polygon", "http", "https://polygon.drpc.org"},
	{"polygon", "http", "https://polygon-rpc.com"},
	{"polygon", "http", "https://1rpc.io/matic"},
	{"polygon", "http", "https://rpc-mainnet.matic.quiknode.pro"},
	{"polygon", "http", "https://polygon.lava.build"},
	{"binance", "http", "https://bsc-mainnet.token.im"},
	{"binance", "http", "https://bsc-dataseed.binance.org"},
	{"binance", "http", "https://bsc-dataseed1.defibit.io"},
	{"binance", "http", "https://bsc-dataseed2.defibit.io"},
	{"binance", "http", "https://bsc-dataseed3.defibit.io"},
	{"binance", "http", "https://binance.llamarpc.com"},
	{"optimism", "http", "https://mainnet.optimism.io"},
	{"optimism", "http", "https://endpoints.omniatech.io/v1/op/mainnet/public"},
	{"arc", "http", "https://rpc.mainnet.arc.io"},
	{"monad", "http", "https://rpc.monad.xyz"},
	{"avalanche", "http", "https://api.avax.network/ext/bc/C/rpc"},
	{"hyperevm", "http", "https://rpc.hyperliquid.xyz/evm"},
	{"plasma", "http", "https://rpc.plasma.to"},
	{"morph", "http", "https://rpc.morphl2.io"},
	{"morph", "http", "https://rpc-bgw.morph.network"},
	{"robinhood", "http", "https://rpc.mainnet.chain.robinhood.com"},
	{"robinhood", "http", "https://robinhood-rpc.publicnode.com"},
	{"robinhood", "http", "https://rpc-robinhood.blockmachine.io"},
	{"robinhood", "http", "https://robinhood.api.pocket.network"},
	{"bitcoin", "unisat", "https://open-api.unisat.io"},
}
