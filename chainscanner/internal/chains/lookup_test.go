package chains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanonicalTransactionPreservesExactContents(t *testing.T) {
	a, _ := canonicalTransaction([]byte(`{"nested":{"b":1,"a":2},"amount":9007199254740993,"logs":[2,1]}`))
	b, _ := canonicalTransaction([]byte(`{ "logs":[2,1], "amount":9007199254740993,"nested":{"a":2,"b":1} }`))
	c, _ := canonicalTransaction([]byte(`{"logs":[1,2],"amount":9007199254740992,"nested":{"a":2,"b":1}}`))
	if string(a) != string(b) || string(b) == string(c) || !strings.Contains(string(a), "9007199254740993") {
		t.Fatal(string(a), string(b), string(c))
	}
}
func TestHashLookupRejectsWrongTransactionOrReceipt(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", 32)
	for _, failure := range []string{"", "wrong transaction", "wrong block", "wrong receipt", "wrong chain"} {
		t.Run(failure, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int
					Method string
				}
				if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
					t.Error(e)
					return
				}
				var value any
				switch req.Method {
				case "eth_chainId":
					value = "0x2105"
					if failure == "wrong chain" {
						value = "0x1"
					}
				case "eth_getTransactionByHash":
					h := hash
					if failure == "wrong transaction" {
						h = "0x0"
					}
					value = map[string]any{"hash": h, "from": "0x1", "input": "0x", "blockHash": hash, "blockNumber": "0x1"}
				case "eth_getTransactionReceipt":
					h, b := hash, hash
					if failure == "wrong receipt" {
						h = "0x0"
					}
					if failure == "wrong block" {
						b = "0xff"
					}
					value = map[string]any{"transactionHash": h, "blockHash": b, "blockNumber": "0x1", "logs": []any{}}
				default:
					t.Errorf("unexpected %s", req.Method)
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": value})
			}))
			defer srv.Close()
			v, e := LookupTransaction(context.Background(), Network{Name: "base", Protocol: "http", URL: srv.URL}, hash, false)
			if (failure == "") != (e == nil) || e == nil && !strings.Contains(string(v), "receipt") {
				t.Fatal(string(v), e)
			}
		})
	}
}
