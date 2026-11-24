package chains

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/gorilla/websocket"
)

// Public directory entries are third-party input. Apply the same dial policy during
// discovery, admin tests and actual scanning, including websocket reconnects.
func PublicRPCProtocol(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.User != nil || u.Hostname() == "" || u.Fragment != "" || strings.ContainsAny(raw, "{}<>$") {
		return ""
	}
	switch u.Scheme {
	case "https":
		return "http"
	case "wss":
		return "ws"
	}
	return ""
}

var specialRoutes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}
var globalIPv6 = netip.MustParsePrefix("2000::/3")

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !globalIPv6.Contains(ip)) {
		return false
	}
	for _, p := range specialRoutes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublic(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	err = errors.New("no public address for RPC endpoint")
	for _, ip := range ips {
		if !publicAddress(ip) {
			continue
		}
		var conn net.Conn
		// Dial the checked IP directly; TLS still validates the URL hostname.
		conn, err = dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}

type limitedRPCTransport struct{ *http.Transport }
type boundedBody struct {
	io.Reader
	io.Closer
}

func (t limitedRPCTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.Transport.RoundTrip(req)
	if err == nil {
		res.Body = boundedBody{io.LimitReader(res.Body, 64<<20), res.Body}
	}
	return res, err
}

func securePublicHTTP(client *http.Client) {
	transport := &http.Transport{
		DialContext: dialPublic, ForceAttemptHTTP2: true,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 8 * time.Second,
	}
	client.Transport = rpcNodeTransport{limitedRPCTransport{transport}}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}
func publicSocketOptions() []rpc.ClientOption {
	return []rpc.ClientOption{rpc.WithWebsocketDialer(websocket.Dialer{NetDialContext: dialPublic, HandshakeTimeout: 8 * time.Second}), rpc.WithWebsocketMessageSizeLimit(64 << 20)}
}

func openPublicRPC(ctx context.Context, n Network, watch bool) (Source, error) {
	p := PublicRPCProtocol(n.URL)
	valid := false
	switch {
	case n.Name == "bitcoin":
		valid = p == "http" && (n.Protocol == "jsonrpc" || n.Protocol == "http")
	case IsEVM(n.Name):
		valid = p != "" && n.Protocol == p
	case n.Name == "solana" || n.Name == "aptos":
		valid = p == "http" && n.Protocol == "http"
	case n.Name == "ton":
		valid = p == "http" && (n.Protocol == "jsonrpc" || n.Protocol == "rest")
	case n.Name == "tron":
		valid = p == "http" && (n.Protocol == "http" || n.Protocol == "solidity-http")
	case n.Name == "sui":
		valid = p == "http" && n.Protocol == "grpc"
	}
	if !valid || n.APIKey != "" || n.HistoryURL != "" || n.HistoryAPIKey != "" {
		return nil, errors.New("invalid public endpoint")
	}
	src, err := newSource(ctx, n, true, watch)
	if err != nil {
		return nil, err
	}
	if jsonRPC, ok := src.(*jsonSource); ok {
		if err := jsonRPC.verifyMainnet(ctx); err != nil {
			src.Close()
			return nil, err
		}
	}

	return src, nil
}

func (s *jsonSource) verifyMainnet(ctx context.Context) error {
	switch s.n.Name {
	case "solana":
		genesis, err := s.call(ctx, "getGenesisHash")
		if err != nil {
			return err
		}
		if genesis.String() != "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d" {
			return errors.New("expected Solana mainnet")
		}
	case "aptos":
		info, err := s.request(ctx, "/v1", nil)
		if err != nil {
			return err
		}
		if info.Get("chain_id").Raw != "1" {
			return errors.New("expected Aptos mainnet")
		}
	case "tron":
		block, err := s.request(ctx, "/wallet/getblockbynum", map[string]int{"num": 0})
		if err != nil {
			return err
		}
		if block.Get("blockID").String() != "00000000000000001ebf88508a03865c71d452e25f4d51194196a1d22b6653dc" {
			return errors.New("expected TRON mainnet")
		}
	}
	return nil
}

func NewPublicSource(ctx context.Context, n Network) (Source, error) {
	return openPublicRPC(ctx, n, true)
}

// Probe a recent numbered block, catching stuck nodes and endpoints that
// answer a height request but cannot serve even the corresponding header.
func ProbePublicRPC(ctx context.Context, n Network) (int64, error) {
	return probePublic(ctx, n, false)
}

// ProbePublicScan exercises the scanner's history read before admission.
func ProbePublicScan(ctx context.Context, n Network) (int64, error) {
	return probePublic(ctx, n, true)
}

func probePublic(ctx context.Context, n Network, scan bool) (int64, error) {
	src, err := openPublicRPC(ctx, n, false)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	if IsEVM(n.Name) {
		h, err := src.(*jsonSource).recentHead(ctx)
		if err == nil && scan {
			_, _, _, err = ReadSpan(ctx, n.Name, src, Span{First: h, Last: h})
		}
		return h, err
	}
	h, err := src.Head(ctx)
	if err != nil || h < 2 {
		return 0, errors.New("missing public chain head")
	}
	span, _, events, err := ReadSpan(ctx, n.Name, src, Span{First: h - 1, Last: h - 1})
	if err != nil {
		return 0, err
	}
	if span.Last != h-1 {
		return 0, errors.New("incomplete recent history")
	}
	maxAge := 5 * time.Minute
	if n.Name == "bitcoin" {
		maxAge = 2 * time.Hour
	}
	for _, event := range events {
		age := time.Since(time.UnixMilli(event.TimeMS))
		if event.TxID != "" && age < maxAge && age > -time.Minute {
			return h, nil
		}
	}
	return 0, errors.New("no recent transaction in sampled history")
}

func (s *jsonSource) recentHead(ctx context.Context) (int64, error) {
	height, err := s.Head(ctx)
	if err != nil {
		return 0, err
	}
	label := "0x" + strconv.FormatInt(height, 16)
	header, err := s.call(ctx, "eth_getBlockByNumber", label, false)
	if err != nil {
		return 0, err
	}
	stamp, err := strconv.ParseInt(header.Get("timestamp").String(), 0, 64)
	age := time.Since(time.Unix(stamp, 0))
	if height <= 0 || err != nil || header.Get("number").String() != label || len(header.Get("hash").String()) != 66 || age > 5*time.Minute || age < -time.Minute {
		return 0, errors.New("stale or invalid EVM header")
	}
	return height, nil
}
