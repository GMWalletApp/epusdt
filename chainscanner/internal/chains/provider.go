package chains

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
)

// Public gateways with documented aliases share one provider identity. Keep different
// operators on branded domains separate (for example, Monad's Goldsky and Ankr RPCs).
var publicGatewayAliases = map[string]string{
	"api.mainnet-beta.solana.com":      "api.mainnet.solana.com",
	"fullnode.mainnet.aptoslabs.com":   "api.mainnet.aptoslabs.com",
	"rpc3.monad.xyz":                   "rpc.ankr.com",
	"bsc-dataseed-public.bnbchain.org": "bsc-dataseed.bnbchain.org",
	"bsc-dataseed.binance.org":         "bsc-dataseed.bnbchain.org",
}

// Different credentials, paths, or transports on one host and port do not
// identify different providers. A paired node uses its history provider's key.
func ProviderKey(n Network) string {
	raw := n.HistoryURL
	if raw == "" {
		raw = n.URL
	}
	u, e := url.Parse(raw)
	if e == nil && u.Hostname() != "" {
		port := u.Port()
		if port == "443" || port == "80" {
			port = ""
		}
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		if alias, ok := publicGatewayAliases[host]; ok && port == "" {
			host = alias
		}
		return n.Name + "|" + host + "|" + port
	}
	return n.Name + "|" + raw
}

// SourceIDs identifies both sides of paired transports. Only opaque identities
// for matched transactions are retained after the scan has committed.
func SourceIDs(n Network) []string {
	if n.URL == "" {
		return nil
	}
	keys := []string{ProviderKey(n)}
	if n.HistoryURL != "" {
		n.HistoryURL = ""
		if key := ProviderKey(n); key != keys[0] {
			keys = append(keys, key)
		}
	}
	if n.Name == "ton" && n.Protocol == "" {
		n.Protocol, _ = EndpointProtocol(n)
	}
	if host, peer, ok := TONVoter(n); ok {
		keys = append(keys, "adnl-host|"+host, "adnl-key|"+peer)
	} else if n.Name == "ton" && n.Protocol == "lite" {
		// A global config may route a block read through any of its LiteServers.
		keys = append(keys, "ton-global-config")
	}
	for i, key := range keys {
		digest := sha256.Sum256([]byte(key))
		keys[i] = hex.EncodeToString(digest[:])
	}
	return keys
}

func SharesScanSource(n Network, scanned map[string]bool) bool {
	if len(scanned) == 0 {
		return false
	}
	if _, _, ok := TONVoter(n); ok {
		global := sha256.Sum256([]byte("ton-global-config"))
		if scanned[hex.EncodeToString(global[:])] {
			return true
		}
	}
	return slices.ContainsFunc(SourceIDs(n), func(key string) bool { return scanned[key] })
}
