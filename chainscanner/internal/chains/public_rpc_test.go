package chains

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestPublicRPCDestinations(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "::ffff:10.0.0.1", "100.64.1.1", "fd00::1", "fe80::1", "2002:7f00:1::1", "64:ff9b::7f00:1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("rejected %s", ip)
		}
	}
	for _, address := range []string{"ipc:///tmp/socket", "ws://rpc.example", "https://user@rpc.example", "https://rpc.example/${API_KEY}"} {
		if PublicRPCProtocol(address) != "" {
			t.Errorf("accepted %s", address)
		}
	}
	for _, url := range []string{"https://127.0.0.1", "wss://127.0.0.1"} {
		_, err := ProbePublicRPC(context.Background(), Network{Name: "base", URL: url, Protocol: PublicRPCProtocol(url)})
		if err == nil {
			t.Fatal("loopback discovery probe succeeded")
		}
	}
}

func TestRecentHead(t *testing.T) {
	for _, tc := range []struct {
		name, number string
		age          time.Duration
		want         int64
	}{
		{"current", "0x2a", time.Minute, 42},
		{"stale", "0x2a", 6 * time.Minute, 0},
		{"future", "0x2a", -2 * time.Minute, 0},
		{"wrong block", "0x2b", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testutil.RPCServer(t, func(req testutil.RPCRequest) (any, error) {
				switch req.Method {
				case "eth_chainId":
					return "0x2105", nil
				case "eth_blockNumber":
					return "0x2a", nil
				case "eth_getBlockByNumber":
					return map[string]string{"number": tc.number, "hash": "0x" + strings.Repeat("a", 64), "timestamp": fmt.Sprintf("0x%x", time.Now().Add(-tc.age).Unix())}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", req.Method)
			})
			defer srv.Close()
			s, err := NewSource(context.Background(), Network{Name: "base", URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.(*jsonSource).recentHead(context.Background())
			if got != tc.want || (err == nil) != (tc.want > 0) {
				t.Fatal(got, err)
			}
		})
	}
}
