package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

var ErrTransactionMissing = errors.New("transaction not found")
var ErrTransactionData = errors.New("incomplete or mismatched transaction")

func TransactionHash(network, raw string) (string, error) {
	if len(raw) > 100 || !Supported(network) {
		return "", ErrTransactionData
	}
	switch network {
	case "bitcoin":
		if bitcoinHash(raw) {
			return strings.ToLower(raw), nil
		}
	case "solana", "sui":
		size := 32
		if network == "solana" {
			size = 64
		}
		if len(base58.Decode(raw)) == size {
			return raw, nil
		}
	default:
		h := strings.TrimPrefix(strings.ToLower(raw), "0x")
		if b, e := hex.DecodeString(h); e == nil && len(b) == 32 {
			if IsEVM(network) || network == "aptos" {
				h = "0x" + h
			}
			return h, nil
		}
		if network == "ton" {
			for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				if b, e := enc.DecodeString(raw); e == nil && len(b) == 32 {
					return hex.EncodeToString(b), nil
				}
			}
		}
	}
	return "", ErrTransactionData
}

// LookupRoute resolves paired interfaces to the actual history endpoint. Stream
// aliases must neither receive duplicate work nor count as additional votes.
func LookupRoute(n Network) (Network, bool) {
	if n.HistoryURL != "" {
		protocol, err := EndpointProtocol(n)
		if err != nil || !n.UsesHistory(protocol) {
			return n, false
		}
		n = Network{Name: n.Name, URL: n.HistoryURL, APIKey: n.HistoryAPIKey, Protocol: "http"}
	}
	switch {
	case IsEVM(n.Name):
		return n, n.Protocol == "http" || n.Protocol == "ws" || n.Protocol == "jsonrpc" || n.Protocol == "ipc"
	case n.Name == "ton":
		return n, n.Protocol == "toncenter-v3"
	case n.Name == "sui":
		return n, n.Protocol == "grpc"
	case n.Name == "tron":
		return n, n.Protocol == "http" || n.Protocol == "rest" || n.Protocol == "solidity-http"
	default:
		return n, n.Protocol == "http" || n.Protocol == "rest" || n.Protocol == "jsonrpc"
	}
}

// Compare and return exactly the same JSON. Preserve integer precision and
// array order, while treating whitespace and object key order as immaterial.
func canonicalTransaction(raw []byte) (json.RawMessage, error) {
	var object map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&object); err != nil || len(object) == 0 {
		return nil, ErrTransactionData
	}
	return json.Marshal(object)
}

func openLookupSource(ctx context.Context, n Network, public bool) (Source, error) {
	if public {
		return openPublicRPC(ctx, n, false)
	}
	return newSource(ctx, n, false, false)
}

// Check availability without sending the caller's transaction hash.
func ProbeLookup(ctx context.Context, n Network, public bool) error {
	if n.Protocol == "toncenter-v3" {
		_, err := TONIndexHeight(ctx, n)
		return err
	}
	src, err := openLookupSource(ctx, n, public)
	if err != nil {
		return err
	}
	defer src.Close()
	if s, ok := src.(*jsonSource); ok && !public {
		if err := s.verifyMainnet(ctx); err != nil {
			return err
		}
	}
	_, err = src.Head(ctx)
	return err
}

func LookupTransaction(ctx context.Context, n Network, hash string, public bool) (json.RawMessage, error) {
	if n.Name == "ton" && n.Protocol == "toncenter-v3" {
		return lookupTON(ctx, n, hash)
	}
	src, err := openLookupSource(ctx, n, public)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if s, ok := src.(*suiSource); ok {
		tx, e := s.finalizedTransaction(ctx, hash, []string{"digest", "checkpoint", "timestamp", "transaction", "signatures", "effects", "events", "balance_changes"})
		if status.Code(e) == codes.NotFound {
			return nil, ErrTransactionMissing
		}
		if e != nil {
			return nil, e
		}
		data, e := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(tx)
		if e != nil {
			return nil, e
		}
		return canonicalTransaction(data)
	}
	s, ok := src.(*jsonSource)
	if !ok {
		return nil, ErrTransactionData
	}
	if !public {
		if e := s.verifyMainnet(ctx); e != nil {
			return nil, e
		}
	}
	var v gjson.Result
	switch {
	case IsEVM(n.Name):
		return s.lookupEVM(ctx, hash)
	case n.Name == "bitcoin":
		return s.lookupBitcoin(ctx, hash)
	case n.Name == "solana":
		v, err = s.call(ctx, "getTransaction", hash, map[string]any{"commitment": "finalized", "encoding": "json", "maxSupportedTransactionVersion": 1})
		if err == nil && v.Raw == "null" {
			// A finalized lookup also returns null for a transaction that is still
			// confirming. Check signature history before reporting it absent.
			status, e := s.call(ctx, "getSignatureStatuses", []string{hash}, map[string]bool{"searchTransactionHistory": true})
			if e != nil {
				return nil, e
			}
			values := status.Get("value").Array()
			if !status.Get("value").IsArray() || len(values) != 1 {
				return nil, ErrTransactionData
			}
			if values[0].Raw == "null" {
				return nil, ErrTransactionMissing
			}
			switch values[0].Get("confirmationStatus").Str {
			case "confirmed", "processed":
				return nil, ErrTransactionPending
			default:
				return nil, ErrTransactionData
			}
		}
		if err == nil && (v.Get("transaction.signatures.0").String() != hash || !v.Get("slot").Exists() || !v.Get("transaction.message").IsObject()) {
			return nil, ErrTransactionData
		}
	case n.Name == "aptos":
		v, err = s.request(ctx, "/v1/transactions/by_hash/"+hash, nil)
		if err == nil && (!strings.EqualFold(v.Get("hash").String(), hash) || v.Get("type").String() == "") {
			return nil, ErrTransactionData
		}
	case n.Name == "tron":
		tx, e := s.request(ctx, "/wallet/gettransactionbyid", map[string]string{"value": hash})
		if e != nil {
			return nil, e
		}
		if tx.IsObject() && len(tx.Map()) == 0 {
			return nil, ErrTransactionMissing
		}
		if !strings.EqualFold(tx.Get("txID").String(), hash) || !tx.Get("raw_data").IsObject() {
			return nil, ErrTransactionData
		}
		info, e := s.request(ctx, "/wallet/gettransactioninfobyid", map[string]string{"value": hash})
		if e != nil {
			return nil, e
		}
		if !info.IsObject() || len(info.Map()) != 0 && !strings.EqualFold(info.Get("id").String(), hash) {
			return nil, ErrTransactionData
		}
		v = gjson.ParseBytes(object(map[string]any{"transaction": json.RawMessage(tx.Raw), "receipt": json.RawMessage(info.Raw)}))
	default:
		return nil, ErrTransactionData
	}
	if err != nil {
		return nil, err
	}
	return canonicalTransaction([]byte(v.Raw))
}

func (s *jsonSource) lookupEVM(ctx context.Context, hash string) (json.RawMessage, error) {
	tx, e := s.call(ctx, "eth_getTransactionByHash", hash)
	if e != nil {
		return nil, e
	}
	if tx.Raw == "null" {
		return nil, ErrTransactionMissing
	}
	if !strings.EqualFold(tx.Get("hash").String(), hash) || !tx.Get("from").Exists() || !tx.Get("input").Exists() {
		return nil, ErrTransactionData
	}
	receipt, e := s.call(ctx, "eth_getTransactionReceipt", hash)
	if e != nil {
		return nil, e
	}
	if _, err := canonicalEVMEvidence(tx, receipt); err != nil {
		return nil, err
	}
	return canonicalTransaction(object(map[string]json.RawMessage{"transaction": json.RawMessage(tx.Raw), "receipt": json.RawMessage(receipt.Raw)}))
}

func lookupTON(ctx context.Context, n Network, hash string) (json.RawMessage, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(10 * time.Second)}
	defer s.Close()
	if _, err := s.tonIndexHeight(ctx); err != nil {
		return nil, err
	}
	return s.tonIndexTransaction(ctx, hash)
}

func (s *jsonSource) tonIndexTransaction(ctx context.Context, hash string) (json.RawMessage, error) {
	v, err := s.request(ctx, "/transactions?hash="+url.QueryEscape(hash)+"&limit=1", nil)
	if err != nil {
		return nil, err
	}
	rows := v.Get("transactions")
	if !rows.IsArray() {
		return nil, ErrTransactionData
	}
	a := rows.Array()
	if len(a) == 0 {
		return nil, ErrTransactionMissing
	}
	h, e := TransactionHash("ton", a[0].Get("hash").String())
	if len(a) != 1 || e != nil || h != hash || !a[0].Get("account").Exists() || !a[0].Get("lt").Exists() {
		return nil, ErrTransactionData
	}
	return canonicalTransaction([]byte(a[0].Raw))
}

func TONIndexHeight(ctx context.Context, n Network) (int64, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(10 * time.Second)}
	defer s.Close()
	return s.tonIndexHeight(ctx)
}
func (s *jsonSource) tonIndexHeight(ctx context.Context) (int64, error) {
	v, e := s.request(ctx, "/masterchainInfo", nil)
	if e != nil {
		return 0, e
	}
	h := v.Get("last.seqno")
	if v.Get("last.global_id").Raw != "-239" || v.Get("last.workchain").Raw != "-1" || h.Type != gjson.Number || h.Int() < 1 {
		return 0, ErrTransactionData
	}
	return h.Int(), nil
}
