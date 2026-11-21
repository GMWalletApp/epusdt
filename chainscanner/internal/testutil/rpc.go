package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type RPCRequest struct {
	ID     json.RawMessage
	Method string
	Params []json.RawMessage
}

func RPCServer(t *testing.T, reply func(RPCRequest) (any, error)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		value, err := reply(req)
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if err != nil {
			response["error"] = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			response["result"] = value
		}
		json.NewEncoder(w).Encode(response)
	}))
}
