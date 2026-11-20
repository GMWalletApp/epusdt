package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"
)

const compareDirectory = "https://www.comparenodes.com/protocols/"

func compareNetworks() []string {
	names := append(slices.Collect(chains.EVMNetworks()), "aptos", "solana", "sui", "ton", "tron")
	slices.Sort(names)
	return names
}

func compareSlug(network string) string {
	switch network {
	case "binance":
		return "bsc"
	case "arbitrum":
		return "arbitrum-one"
	}
	return network
}

func (r *NodeRegistry) refreshCompareNodes(ctx context.Context, client *http.Client) {
	// Independent chains progress concurrently, with at most 16 candidate probes.
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for network := range jobs {
				entries, err := fetchCompareNodes(ctx, client, compareDirectory, network)
				if err == nil {
					_, err = r.refreshCandidates(ctx, "comparenodes", network, entries, chains.ProbePublicScan)
				}
				if err != nil && ctx.Err() == nil {
					log.Printf("CompareNodes %s refresh: %v", network, err)
				}
			}
		})
	}
	for _, name := range compareNetworks() {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- name:
		}
	}
	close(jobs)
	wg.Wait()
}

type compareRow struct {
	ChainSlug, ProviderSlug, CategoryPath, Network, Endpoint string
	IsMainnet, IsPremium                                     *bool
}

func fetchCompareNodes(ctx context.Context, client *http.Client, base, network string) ([]NodeEndpoint, error) {
	slug := compareSlug(network)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+slug+"/", nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog HTTP %d", res.StatusCode)
	}
	const limit = 16 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, errors.New("catalog exceeds size limit")
	}
	return parseCompareNodes(body, network)
}

// The protocol page embeds the public endpoint table as Next.js page data.
func comparePageData(body []byte) ([]byte, error) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return nil, errors.New("missing CompareNodes directory data")
		case html.StartTagToken:
			name, attrs := z.TagName()
			if string(name) != "script" {
				continue
			}
			for attrs {
				k, v, more := z.TagAttr()
				attrs = more
				if string(k) == "id" && string(v) == "__NEXT_DATA__" {
					if z.Next() != html.TextToken {
						return nil, errors.New("empty CompareNodes page data")
					}
					return z.Text(), nil
				}
			}
		}
	}
}

func parseCompareNodes(body []byte, network string) ([]NodeEndpoint, error) {
	data, err := comparePageData(body)
	if err != nil {
		return nil, err
	}
	slug := compareSlug(network)
	if !gjson.ValidBytes(data) || gjson.GetBytes(data, "query.blockchain").String() != slug {
		return nil, errors.New("invalid CompareNodes page data or chain")
	}
	table := gjson.GetBytes(data, "props.pageProps.blockchainProps.publicEndpointTableData.tableData")
	var rows []compareRow
	if !table.IsArray() || json.Unmarshal([]byte(table.Raw), &rows) != nil {
		return nil, errors.New("missing or invalid CompareNodes endpoint table")
	}
	var entries []NodeEndpoint
	seen := map[NodeEndpoint]bool{}
	for _, row := range rows {
		if row.ChainSlug != slug || row.IsMainnet == nil || row.IsPremium == nil || row.Network == "" || row.Endpoint == "" || row.CategoryPath == "" || row.ProviderSlug == "" {
			return nil, errors.New("incomplete CompareNodes directory row")
		}
		if !*row.IsMainnet || *row.IsPremium {
			continue
		}
		if e, ok := row.endpoint(network); ok && !seen[e] {
			entries = append(entries, e)
			seen[e] = true
		}
	}
	return entries, nil
}

// Directory entries sometimes embed shared API keys even in the free tier.
// Only admit known public route components and optional chain routing parameters.
func comparePublicRoute(u *url.URL, network string) bool {
	if u.RawPath != "" || u.User != nil || u.Fragment != "" || u.ForceQuery {
		return false
	}
	id := strconv.FormatInt(chains.EVMChainID(network), 10)
	if u.RawQuery != "" {
		k, v, ok := strings.Cut(u.RawQuery, "=")
		if !ok || k != "chainId" || !(v == network || v == compareSlug(network) || (network == "ton" && v == "toncoin") || (chains.IsEVM(network) && v == id)) {
			return false
		}
	}
	for seg := range strings.SplitSeq(strings.ToLower(u.Path), "/") {
		seg = strings.TrimSuffix(strings.TrimSuffix(seg, "_mainnet"), "-mainnet")
		switch seg {
		case "", "v1", "v2", "ws", "websocket", "rpc", "public", "public-ws", "json-rpc", "jsonrpc", "mainnet", "api", "uni", "evm", "ext", "bc", "c", "wallet", "walletsolidity", "fullprivacy", "maxbackrun", "fast", "eth", "bsc", "bnb", "matic", "arb", "hl", "op", "hyperliquid", "avax":
		default:
			if seg != network && seg != compareSlug(network) && !(chains.IsEVM(network) && seg == id) {
				return false
			}
		}
	}
	return true
}

func (r compareRow) endpoint(network string) (NodeEndpoint, bool) {
	u, err := url.Parse(r.Endpoint)
	if err != nil || !comparePublicRoute(u, network) {
		return NodeEndpoint{}, false
	}
	kind := strings.TrimSuffix(r.CategoryPath, "|NC")
	if network == "sui" && kind == "Sui|gRPC" && u.Scheme == "grpcs" {
		u.Scheme = "https"
	}
	p := chains.PublicRPCProtocol(u.String())
	if p == "" {
		return NodeEndpoint{}, false
	}
	switch {
	case chains.IsEVM(network) && kind == "EVM":
		if network == "avalanche" && r.Network != "Mainnet C-chain" {
			return NodeEndpoint{}, false
		}
	case network == "solana" && kind == "SVM" && p == "http":
		if r.ProviderSlug == "uniblock" || r.ProviderSlug == "tatum" {
			return NodeEndpoint{}, false
		}
	case network == "aptos" && kind == "Aptos/Movement" && p == "http":
	case network == "sui" && kind == "Sui|gRPC" && p == "http":
		p = "grpc"
	case network == "ton" && kind == "ton" && p == "http":
		p = "jsonrpc"
		if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api/v2") {
			p = "rest"
		}
	case network == "tron" && p == "http" && (kind == "EVM|Tron HTTP" || kind == "EVM|Tron Solidity"):
		if kind == "EVM|Tron Solidity" {
			p = "solidity-http"
		}
		u.Path = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/walletsolidity"), "/wallet")
	default:
		return NodeEndpoint{}, false
	}
	return (NodeEndpoint{network, p, u.String()}).identity(), true
}
