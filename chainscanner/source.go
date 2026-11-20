package scanner

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
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/address"
)

func IsEVM(n string) bool {
	switch n {
	case "ethereum", "binance", "polygon", "plasma":
		return true
	}
	return false
}
func Canonical(n, a string) (string, error) {
	a = strings.TrimSpace(a)
	switch {
	case IsEVM(n):
		if common.IsHexAddress(a) {
			return strings.ToLower(common.HexToAddress(a).Hex()), nil
		}
	case n == "aptos":
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
func safeError(err error, n Network) string {
	s := strings.ReplaceAll(err.Error(), n.URL, "<rpc>")
	if n.APIKey != "" {
		s = strings.ReplaceAll(s, n.APIKey, "<key>")
	}
	return s
}

type jsonSource struct {
	n    Network
	rpc  *rpc.Client
	http *http.Client
}

func NewSource(ctx context.Context, n Network) (Source, error) {
	u, err := url.Parse(n.URL)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid RPC URL")
	}
	if n.Name == "ton" {
		return newTON(ctx, n)
	}
	s := &jsonSource{n: n, http: &http.Client{Timeout: 25 * time.Second}}
	switch {
	case IsEVM(n.Name) || n.Name == "solana":
		opts := []rpc.ClientOption{rpc.WithHTTPClient(s.http)}
		if n.APIKey != "" {
			opts = append(opts, rpc.WithHeader("Authorization", "Bearer "+n.APIKey))
		}
		c, err := rpc.DialOptions(ctx, n.URL, opts...)
		if err != nil {
			return nil, err
		}
		s.rpc = c
	case n.Name == "tron" || n.Name == "aptos":
	default:
		return nil, fmt.Errorf("unsupported network %s", n.Name)
	}
	return s, nil
}
func (s *jsonSource) Close() {
	if s.rpc != nil {
		s.rpc.Close()
	}
	s.http.CloseIdleConnections()
}
func (s *jsonSource) request(ctx context.Context, path string, post any) (gjson.Result, error) {
	if s.n.Name == "aptos" && strings.HasSuffix(strings.TrimRight(s.n.URL, "/"), "/v1") {
		path = strings.TrimPrefix(path, "/v1")
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
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.n.URL, "/")+path, body)
	if err != nil {
		return gjson.Result{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.n.APIKey != "" {
		if s.n.Name == "tron" {
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
		return gjson.Result{}, fmt.Errorf("RPC HTTP %d", res.StatusCode)
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
	return strconv.ParseInt(v.String(), 0, 64)
}
func (s *jsonSource) Read(ctx context.Context, next, head int64) (int64, []Activity, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	switch s.n.Name {
	case "aptos":
		return s.aptos(ctx, next, head)
	case "tron":
		return s.tron(ctx, next)
	case "solana":
		return s.solana(ctx, next, head)
	default:
		return s.evm(ctx, next)
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
	Transaction json.RawMessage   `json:"transaction"`
	Logs        []json.RawMessage `json:"logs,omitempty"`
	Receipt     json.RawMessage   `json:"receipt,omitempty"`
	Trace       json.RawMessage   `json:"trace,omitempty"`
}

func indexTopics(a *[]string, n string, topics []gjson.Result) {
	for _, topic := range topics {
		h := strings.TrimPrefix(topic.String(), "0x")
		if len(h) == 64 && strings.HasPrefix(h, "000000000000000000000000") {
			addAddress(a, n, h[24:])
		}
	}
}
