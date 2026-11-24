package chains

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestCanonicalTransactionPreservesExactContents(t *testing.T) {
	a, _ := canonicalTransaction([]byte(`{"nested":{"b":1,"a":2},"amount":9007199254740993,"logs":[2,1]}`))
	b, _ := canonicalTransaction([]byte(`{ "logs":[2,1], "amount":9007199254740993,"nested":{"a":2,"b":1} }`))
	c, _ := canonicalTransaction([]byte(`{"logs":[1,2],"amount":9007199254740992,"nested":{"a":2,"b":1}}`))
	if string(a) != string(b) || string(b) == string(c) || !strings.Contains(string(a), "9007199254740993") {
		t.Fatal(string(a), string(b), string(c))
	}
}
func TestEVMEvidenceIgnoresProviderOptionalFields(t *testing.T) {
	txA := gjson.Parse(`{
		"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10",
		"from":"0x1111111111111111111111111111111111111111",
		"to":"0x2222222222222222222222222222222222222222",
		"input":"0x","value":"0x1","blockTimestamp":"0x1234",
		"gas":"0x5208","gasPrice":"0x1","type":"0x2"
	}`)
	txB := gjson.Parse(`{
		"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10",
		"from":"0x1111111111111111111111111111111111111111",
		"to":"0x2222222222222222222222222222222222222222",
		"input":"0x","value":"0x1"
	}`)
	receiptA := gjson.Parse(`{
		"transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10","status":"0x1",
		"effectiveGasPrice":"0x1","logsBloom":"0x0",
		"logs":[{"address":"0x3333333333333333333333333333333333333333","topics":["0x0000000000000000000000000000000000000000000000000000000000000001"],"data":"0x02","transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","logIndex":"0x0","removed":false}]
	}`)
	receiptB := gjson.Parse(`{
		"transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10","status":"0x1",
		"logs":[{"address":"0x3333333333333333333333333333333333333333","topics":["0x0000000000000000000000000000000000000000000000000000000000000001"],"data":"0x02","transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","removed":false}]
	}`)
	a, err := canonicalEVMEvidence(txA, receiptA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalEVMEvidence(txB, receiptB)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("optional provider fields changed evidence:\n%s\n%s", a, b)
	}
	if strings.Contains(string(a), "blockTimestamp") || strings.Contains(string(a), "effectiveGasPrice") || strings.Contains(string(a), "logIndex") {
		t.Fatalf("optional field leaked into evidence: %s", a)
	}
	bad := gjson.Parse(`{"transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10","status":"0x0","logs":[]}`)
	c, err := canonicalEVMEvidence(txB, bad)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(c) {
		t.Fatal("receipt status mismatch was ignored")
	}
	if _, err := canonicalEVMEvidence(txB, gjson.Parse(`{"transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","blockHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","blockNumber":"0x10","logs":[]}`)); err == nil {
		t.Fatal("missing receipt status was accepted")
	}
}

func TestHashLookupRejectsWrongTransactionOrReceipt(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", 32)
	for _, failure := range []string{"", "equivalent block", "wrong transaction", "wrong block", "wrong receipt", "wrong chain"} {
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
					value = map[string]any{"hash": h, "from": hash[:42], "to": hash[:42], "input": "0x", "value": "0x0", "blockHash": hash, "blockNumber": "0x1"}
				case "eth_getTransactionReceipt":
					h, b := hash, hash
					if failure == "wrong receipt" {
						h = "0x0"
					}
					if failure == "wrong block" {
						b = "0xff"
					}
					number := "0x1"
					if failure == "equivalent block" {
						b = strings.ToUpper(b)
						number = "0x01"
					}
					value = map[string]any{"transactionHash": h, "blockHash": b, "blockNumber": number, "status": "0x1", "logs": []any{}}
				default:
					t.Errorf("unexpected %s", req.Method)
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": value})
			}))
			defer srv.Close()
			v, e := LookupTransaction(context.Background(), Network{Name: "base", Protocol: "http", URL: srv.URL}, hash, false)
			if (failure == "" || failure == "equivalent block") != (e == nil) || e == nil && !strings.Contains(string(v), "receipt") {
				t.Fatal(string(v), e)
			}
		})
	}
}

func TestAptosTransactionMissingRequiresStructuredNotFound(t *testing.T) {
	for _, tc := range []struct {
		body    string
		missing bool
	}{
		{`{"error_code":"transaction_not_found"}`, true},
		{"<html>Not Found</html>", false},
		{`{"error_code":"api_not_found"}`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { http.Error(w, tc.body, http.StatusNotFound) }))
		s := jsonSource{n: Network{Name: "aptos", URL: srv.URL + "/v1"}, http: srv.Client()}
		_, err := s.request(t.Context(), "/v1/transactions/by_hash/hash", nil)
		srv.Close()
		if err == nil || errors.Is(err, ErrTransactionMissing) != tc.missing {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
}
