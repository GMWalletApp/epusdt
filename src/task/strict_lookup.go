package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/service"
)

type strictClient struct {
	client    scanner.Client
	transport *http.Client
}

func newStrictClient(client scanner.Client) *strictClient {
	transport := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &strictClient{client, transport}
}
func (c *strictClient) full(ctx context.Context, network, hash string) (json.RawMessage, error) {
	return c.query(ctx, network, hash, true)
}
func (c *strictClient) automatic(ctx context.Context, network, hash string) (json.RawMessage, error) {
	return c.query(ctx, network, hash, false)
}
func (c *strictClient) query(ctx context.Context, network, hash string, manual bool) (json.RawMessage, error) {
	if !scanner.Supported(network) || hash == "" || strings.ContainsAny(hash, "/?#") {
		return nil, errors.New("invalid transaction lookup")
	}
	target := strings.TrimRight(c.client.URL, "/") + "/v1/transactions/" + url.PathEscape(network) + "/" + url.PathEscape(hash)
	if manual {
		target += "?manual=true"
	} else {
		target += "?observed=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.client.Token)
	res, err := c.transport.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	source := res.Header.Get("X-ChainScanner-Scan-Source")
	independent := source == "excluded" || manual && source == "unobserved"
	if res.StatusCode == http.StatusNotFound && independent && res.Header.Get("X-ChainScanner-Evidence") == "not_found" {
		return nil, service.ErrTransactionNotFound
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("ChainScanner strict lookup HTTP %d", res.StatusCode)
	}
	if !independent {
		return nil, errors.New("ChainScanner did not confirm scan-source exclusion")
	}
	var body struct {
		Network, Hash string
		Result        json.RawMessage
	}
	if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Network != network || body.Hash != hash || len(body.Result) == 0 || body.Result[0] != '{' {
		return nil, errors.New("invalid strict transaction response")
	}
	return scanner.PaymentEvidence(network, body.Result)
}
