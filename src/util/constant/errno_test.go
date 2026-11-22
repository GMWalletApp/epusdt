package constant

import (
	"encoding/json"
	"os"
	"testing"
)

// The public registry is also consumed by the API reference and merchant SDKs.
func TestPublishedErrorCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../docs/error-codes.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[int]struct {
		HTTPStatus int    `json:"http_status"`
		Message    string `json:"message"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != len(Errno) {
		t.Fatal("error catalog size differs from runtime registry")
	}
	for code, message := range Errno {
		entry, ok := catalog[code]
		if !ok || entry.Message != message || entry.HTTPStatus != Err(code).(*RspError).HttpStatus() {
			t.Errorf("catalog mismatch for %d", code)
		}
	}
}
