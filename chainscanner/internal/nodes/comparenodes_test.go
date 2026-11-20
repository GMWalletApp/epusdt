package nodes

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestCompareDirectoryFiltering(t *testing.T) {
	page := func(chain, rows string) []byte {
		return []byte(fmt.Sprintf(`<html><script type="application/json" id="__NEXT_DATA__">{"query":{"blockchain":%q},"props":{"pageProps":{"blockchainProps":{"publicEndpointTableData":{"tableData":%s}}}}}</script></html>`, chain, rows))
	}
	row := func(host string, flags string) string {
		return fmt.Sprintf(`{"endpoint":%q,"providerSlug":"vendor","chainSlug":"solana","categoryPath":"SVM","network":"Mainnet",%s}`, host, flags)
	}
	const free = `"isPremium":false,"isMainnet":true`
	rows := "[" + row("https://a.example:443/", free) + "," + row("https://A.example", free) + "," + row("https://paid.example", `"isPremium":true,"isMainnet":true`) + "," + row("https://test.example", `"isPremium":false,"isMainnet":false`) + "]"
	got, err := parseCompareNodes(page("solana", rows), "solana")
	if err != nil || len(got) != 1 || got[0].URL != "https://a.example" {
		t.Fatal(got, err)
	}
	for _, body := range [][]byte{page("solana", "null"), page("solana", "["+row("https://a.example", `"isMainnet":true`)+"]"), page("other", "[]"), []byte("<html>maintenance</html>")} {
		if _, err := parseCompareNodes(body, "solana"); err == nil {
			t.Fatal("accepted invalid snapshot")
		}
	}
	if got, err := parseCompareNodes(page("solana", "[]"), "solana"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestCompareProtocols(t *testing.T) {
	for _, tc := range []struct{ network, category, endpoint, provider, label, want string }{
		{"arbitrum", "EVM|NC", "wss://a.example/ws", "", "Mainnet", "ws"},
		{"avalanche", "EVM", "https://a.example/ext/bc/C/rpc", "", "Mainnet C-chain", "http"},
		{"avalanche", "EVM", "https://a.example/", "", "Mainnet P-chain", ""},
		{"ethereum", "EVM|Beacon API", "https://a.example", "", "Mainnet", ""},
		{"sui", "Sui|gRPC", "grpcs://a.example:443", "", "Mainnet", "grpc"},
		{"sui", "Sui|gRPC-Web", "https://a.example", "", "Mainnet", ""},
		{"tron", "EVM|Tron Solidity", "https://a.example/walletsolidity", "", "Mainnet", "solidity-http"},
		{"tron", "EVM|Tron HTTP", "https://a.example/wallet", "", "Mainnet", "http"},
		{"ton", "ton", "https://a.example/api/v2/jsonRPC", "", "Mainnet", "jsonrpc"},
		{"ton", "ton", "https://a.example/api/v2", "", "Mainnet", "rest"},
		{"aptos", "Aptos/Movement", "https://a.example/v1/public", "", "Mainnet", "http"},
		{"solana", "SVM", "https://a.example", "uniblock", "Mainnet", ""},
		{"solana", "SVM", "https://a.example", "tatum", "Mainnet", ""},
		{"solana", "SVM", "https://a.example/?api_key=FREE", "", "Mainnet", ""},
		{"binance", "EVM", "https://a.example/uni/v1/json-rpc?chainId=56", "", "Mainnet", "http"},
		{"binance", "EVM", "https://a.example/uni/v1/json-rpc?chainId=1", "", "Mainnet", ""},
		{"base", "EVM", "https://a.example/v1/71ef932b9fbe5bac2d99474018c29c00", "", "Mainnet", ""},
		{"base", "EVM", "https://user:secret@a.example", "", "Mainnet", ""},
	} {
		row := compareRow{CategoryPath: tc.category, Endpoint: tc.endpoint, ProviderSlug: tc.provider, Network: tc.label}
		e, ok := row.endpoint(tc.network)
		if ok != (tc.want != "") || e.Protocol != tc.want {
			t.Errorf("%+v => %+v", tc, e)
		}
		if ok {
			if err := chains.ValidateNetwork(chains.Network{Name: e.Network, Protocol: e.Protocol, URL: e.URL}); err != nil {
				t.Error(tc, err)
			}
		}
	}
}

func TestCompareRetainsHealthAndRespectsOwnership(t *testing.T) {
	ctx := context.Background()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := NodeEndpoint{"solana", "http", "https://a.example"}
	protected := NodeEndpoint{"solana", "http", "https://solana-rpc.publicnode.com"}
	bad := NodeEndpoint{"solana", "http", "https://offline.example"}
	calls := 0
	update := func(want int, entries ...NodeEndpoint) {
		t.Helper()
		count, err := r.refreshCandidates(ctx, "comparenodes", "solana", entries, func(_ context.Context, n chains.Network) (int64, error) {
			if n.URL == bad.URL {
				return 0, errors.New("read rejected")
			}
			if n.URL != e.URL {
				t.Error("probed protected node", n)
			}
			calls++
			return 456, nil
		})
		if err != nil || count != want {
			t.Fatalf("count=%d want=%d err=%v", count, want, err)
		}
	}
	update(1, e, protected, bad)
	update(1, e, protected)
	if calls != 1 {
		t.Fatal("reprobed a known node")
	}
	update(0)
	update(1, e)
	ns, err := r.List(ctx, "solana")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, n := range ns {
		if n.URL == e.URL {
			id = n.ID
		}
	}
	if err = r.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	update(0, e)
}
