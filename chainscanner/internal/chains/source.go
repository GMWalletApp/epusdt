package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/address"
)

func Canonical(n, a string) (string, error) {
	a = strings.TrimSpace(a)
	switch {
	case n == "bitcoin":
		return bitcoinAddress(a)
	case IsEVM(n):
		if common.IsHexAddress(a) {
			return strings.ToLower(common.HexToAddress(a).Hex()), nil
		}
	case n == "aptos" || n == "sui":
		h := strings.TrimPrefix(a, "0x")
		if len(h) > 0 && len(h) <= 64 {
			h = strings.Repeat("0", 64-len(h)) + h
			if _, e := hex.DecodeString(h); e == nil {
				return "0x" + strings.ToLower(h), nil
			}
		}
	case n == "tron":
		if p, v, e := base58.CheckDecode(a); e == nil && v == 65 && len(p) == 20 {
			return a, nil
		}
		h := strings.TrimPrefix(a, "0x")
		if len(h) == 42 && h[:2] == "41" {
			h = h[2:]
		}
		if p, e := hex.DecodeString(h); e == nil && len(p) == 20 {
			return base58.CheckEncode(p, 65), nil
		}
	case n == "solana":
		if len(base58.Decode(a)) == 32 {
			return a, nil
		}
	case n == "ton":
		var p *address.Address
		var err error
		if strings.Contains(a, ":") {
			p, err = address.ParseRawAddr(a)
		} else {
			p, err = address.ParseAddr(a)
		}
		if err == nil && p != nil && p.Type() == address.StdAddress && !p.IsTestnetOnly() {
			return p.StringRaw(), nil
		}
	}
	return "", fmt.Errorf("invalid address or network %q", n)
}
func RedactError(err error, n Network) string {
	return redactEndpointError(err.Error(), n)
}

var ErrRateLimited = errors.New("RPC HTTP 429")

// RPC credentials belong to one origin, including provider-specific headers.
func newNodeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: rpcNodeTransport{http.DefaultTransport}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

type jsonSource struct {
	n             Network
	rpc           *rpc.Client
	http          *http.Client
	heads         *headSignals
	finalizedSlot atomic.Int64
	slots         atomic.Pointer[slotWindow]
	catchup       pairedReads
}

func NewSource(ctx context.Context, n Network) (Source, error) {
	src, err := newSource(ctx, n, false, true)
	if err != nil {
		return nil, err
	}
	if s, ok := src.(*jsonSource); ok && n.Name == "solana" {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := s.verifyMainnet(probe); err != nil {
			s.Close()
			return nil, err
		}
	}
	return src, nil
}

func newSource(ctx context.Context, n Network, discovered, watch bool) (Source, error) {
	if err := ValidateNetwork(n); err != nil {
		return nil, err
	}
	u, p, _ := n.endpoint()
	if p == "graphql" && n.Name != "aptos" {
		return newGraph(ctx, n)
	}
	if n.UsesHistory(p) {
		return newPaired(ctx, n, u, p)
	}
	if n.Name == "sui" {
		return newSui(ctx, n, u, discovered, watch)
	}
	if n.Name == "ton" && p != "lite" {
		return newTonCenter(ctx, n, p, discovered)
	}
	if n.Name == "ton" {
		return newTON(ctx, n)
	}
	if n.Name == "tron" && (p == "grpc" || p == "solidity-grpc") {
		return newTronGRPC(ctx, n, u)
	}
	s := &jsonSource{n: n, http: newNodeHTTPClient(25 * time.Second)}
	if discovered {
		securePublicHTTP(s.http)
	}
	switch {
	case IsEVM(n.Name) || n.Name == "solana" || n.Name == "bitcoin":
		opts := []rpc.ClientOption{rpc.WithHTTPClient(s.http)}
		if discovered {
			opts = append(opts, publicSocketOptions()...)
		}
		if n.APIKey != "" {
			opts = append(opts, rpc.WithHeader("Authorization", "Bearer "+n.APIKey))
		}
		endpoint := n.URL
		if p == "ipc" {
			info, e := os.Stat(u.Path)
			if e != nil || info.Mode()&os.ModeSocket == 0 {
				return nil, errors.New("IPC endpoint must be a Unix socket")
			}
			endpoint = u.Path
		}
		c, err := rpc.DialOptions(ctx, endpoint, opts...)
		if err != nil {
			return nil, err
		}
		s.rpc = c
		if n.Name == "bitcoin" {
			v, e := s.call(ctx, "getblockhash", 0)
			if e != nil || v.Str != bitcoinGenesis {
				s.Close()
				return nil, errors.Join(errors.New("expected Bitcoin mainnet genesis"), e)
			}
		}
		if IsEVM(n.Name) {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			id, e := s.call(probe, "eth_chainId")
			actual, parseErr := strconv.ParseInt(id.String(), 0, 64)
			if e != nil {
				s.Close()
				return nil, fmt.Errorf("chain ID check: %w", e)
			}
			if parseErr != nil || actual != EVMChainID(n.Name) {
				s.Close()
				return nil, fmt.Errorf("chain ID check failed for %s", n.Name)
			}
			if watch && (p == "ws" || p == "ipc") {
				s.heads = startSignals(ctx, func(ctx context.Context, wake func()) error {
					return evmHeads(ctx, c, func(int64) { wake() })
				})
			}
		}
	case n.Name == "tron" || n.Name == "aptos":
	default:
		return nil, fmt.Errorf("unsupported network %s", n.Name)
	}
	return s, nil
}
func (s *jsonSource) Close() {
	if s.heads != nil {
		s.heads.stop()
	}
	if s.rpc != nil {
		s.rpc.Close()
	}
	s.http.CloseIdleConnections()
}
func (s *jsonSource) request(ctx context.Context, path string, post any) (gjson.Result, error) {
	base, err := url.Parse(s.n.URL)
	if err != nil {
		return gjson.Result{}, err
	}
	if s.n.Name == "aptos" {
		root := strings.TrimRight(base.Path, "/")
		// OnFinality exposes the versioned REST API directly at /v1/public.
		if strings.HasSuffix(root, "/v1") || (strings.EqualFold(base.Hostname(), "aptos.api.onfinality.io") && root == "/v1/public") {
			path = strings.TrimPrefix(path, "/v1")
		}
	}
	method := "GET"
	var body io.Reader
	if post != nil {
		method = "POST"
		b, err := json.Marshal(post)
		if err != nil {
			return gjson.Result{}, err
		}
		body = bytes.NewReader(b)
	}
	if s.n.Protocol == "solidity-http" && strings.HasPrefix(path, "/wallet/") {
		path = "/walletsolidity/" + strings.TrimPrefix(path, "/wallet/")
	}
	if path != "" {
		suffix, query, _ := strings.Cut(path, "?")
		base.Path = strings.TrimRight(base.Path, "/") + suffix
		if query != "" {
			if base.RawQuery != "" {
				base.RawQuery += "&"
			}
			base.RawQuery += query
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return gjson.Result{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.n.APIKey != "" {
		if s.n.Name == "ton" {
			request.Header.Set("X-API-Key", s.n.APIKey)
		} else if s.n.Name == "tron" {
			request.Header.Set("TRON-PRO-API-KEY", s.n.APIKey)
		} else {
			request.Header.Set("Authorization", "Bearer "+s.n.APIKey)
		}
	}
	res, err := s.http.Do(request)
	if err != nil {
		return gjson.Result{}, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		if res.StatusCode == http.StatusTooManyRequests {
			return gjson.Result{}, ErrRateLimited
		}
		if s.n.Name == "aptos" && res.StatusCode == http.StatusNotFound && strings.HasPrefix(strings.TrimPrefix(path, "/v1"), "/transactions/by_hash/") {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 8192))
			if readErr == nil && gjson.ValidBytes(body) && gjson.GetBytes(body, "error_code").Str == "transaction_not_found" {
				return gjson.Result{}, ErrTransactionMissing
			}
		}
		return gjson.Result{}, rpc.HTTPError{StatusCode: res.StatusCode, Status: res.Status}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (64<<20)+1))
	if err != nil {
		return gjson.Result{}, err
	}
	if len(b) > 64<<20 || !gjson.ValidBytes(b) {
		return gjson.Result{}, errors.New("invalid RPC response")
	}
	return gjson.ParseBytes(b), nil
}
func (s *jsonSource) call(ctx context.Context, method string, params ...any) (gjson.Result, error) {
	var raw json.RawMessage
	err := s.rpc.CallContext(ctx, &raw, method, params...)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(raw), nil
}
func (s *jsonSource) Head(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var v gjson.Result
	var err error
	switch {
	case IsEVM(s.n.Name):
		v, err = s.call(ctx, "eth_blockNumber")
	case s.n.Name == "bitcoin":
		v, err = s.call(ctx, "getblockcount")
	case s.n.Name == "solana":
		v, err = s.call(ctx, "getSlot", map[string]string{"commitment": "finalized"})
	case s.n.Name == "aptos":
		v, err = s.request(ctx, "/v1", nil)
		v = v.Get("ledger_version")
	case s.n.Name == "tron":
		v, err = s.request(ctx, "/wallet/getnowblock", map[string]any{})
		v = v.Get("block_header.raw_data.number")
	}
	if err != nil {
		return 0, err
	}
	height, err := strconv.ParseInt(v.String(), 0, 64)
	if s.n.Name == "solana" && err == nil {
		s.finalizedSlot.Store(height)
	}
	return height, err
}
func (s *jsonSource) Read(ctx context.Context, next, head int64) (int64, []Activity, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	switch s.n.Name {
	case "bitcoin":
		return s.bitcoinBlock(ctx, next, nil)
	case "aptos":
		return s.aptos(ctx, next, head)
	case "tron":
		return s.tron(ctx, next)
	case "solana":
		return s.solana(ctx, next, head)
	default:
		return s.evmRange(ctx, next, head)
	}
}
func addAddress(a *[]string, n, v string) {
	if c, e := Canonical(n, v); e == nil {
		*a = append(*a, c)
	}
}
func object(data any) json.RawMessage {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}

type Evidence struct {
	RefreshAssets []string          `json:"refresh_assets,omitempty"`
	Transaction   json.RawMessage   `json:"transaction"`
	Logs          []json.RawMessage `json:"logs,omitempty"`
	Receipt       json.RawMessage   `json:"receipt,omitempty"`
	Trace         json.RawMessage   `json:"trace,omitempty"`
}

func indexTopics(a *[]string, n string, topics []gjson.Result) {
	for _, topic := range topics {
		h := strings.TrimPrefix(topic.String(), "0x")
		if len(h) == 64 && strings.HasPrefix(h, "000000000000000000000000") {
			addAddress(a, n, h[24:])
		}
	}
}
