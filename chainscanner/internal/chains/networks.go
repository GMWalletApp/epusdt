package chains

import (
	"iter"
	"maps"
)

// These names identify separate ledgers even when wallet addresses look alike.
var evmIDs = map[string]int64{
	"ethereum": 1, "binance": 56, "polygon": 137, "plasma": 9745,
	"arbitrum": 42161, "hyperevm": 999, "monad": 143, "arc": 5042, "avalanche": 43114, "optimism": 10, "base": 8453,
	"robinhood": 4663,
}

func IsEVM(name string) bool       { _, ok := evmIDs[name]; return ok }
func EVMChainID(name string) int64 { return evmIDs[name] }
func Supported(name string) bool {
	return name == "bitcoin" || IsEVM(name) || name == "aptos" || name == "sui" || name == "solana" || name == "ton" || name == "tron"
}

// NativeAsset identifies the top-level value-transfer assets supported by
// the new EVM adapters. All use 18 decimals for JSON-RPC transaction value.
func NativeAsset(name string) string {
	switch name {
	case "arbitrum", "optimism", "base", "robinhood":
		return "ETH"
	case "hyperevm":
		return "HYPE"
	case "monad":
		return "MON"
	case "avalanche":
		return "AVAX"
	case "arc":
		return "USDC"
	}
	return ""
}

func EVMNetworks() iter.Seq[string] { return maps.Keys(evmIDs) }
