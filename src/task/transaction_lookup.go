package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/service"
)

type transactionClient struct {
	client    scanner.Client
	transport *http.Client
}

func newTransactionClient(client scanner.Client) *transactionClient {
	transport := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &transactionClient{client, transport}
}
func (c *transactionClient) full(ctx context.Context, network, hash string) (json.RawMessage, error) {
	return c.query(ctx, network, hash, true, 0)
}
func (c *transactionClient) automatic(ctx context.Context, network, hash string, height int64) (json.RawMessage, error) {
	return c.query(ctx, network, hash, false, height)
}
func (c *transactionClient) query(ctx context.Context, network, hash string, manual bool, height int64) (json.RawMessage, error) {
	if !scanner.Supported(network) || hash == "" || strings.ContainsAny(hash, "/?#") {
		return nil, errors.New("invalid transaction lookup")
	}
	target := strings.TrimRight(c.client.URL, "/") + "/v1/transactions/" + url.PathEscape(network) + "/" + url.PathEscape(hash)
	if manual {
		target += "?manual=true"
	} else {
		target += "?observed=true&observed_height=" + strconv.FormatInt(height, 10)
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
	observed := source == "excluded" || source == "fallback_reused"
	accepted := observed || manual && source == "unobserved"
	heightChecked := res.Header.Get("X-ChainScanner-Height-Checked") == "true"
	if res.StatusCode == http.StatusNotFound && accepted && res.Header.Get("X-ChainScanner-Evidence") == "not_found" && (heightChecked || !observed) {
		return nil, service.ErrTransactionNotFound
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("ChainScanner transaction lookup HTTP %d", res.StatusCode)
	}
	if !accepted {
		return nil, errors.New("ChainScanner did not confirm an eligible lookup source")
	}
	var body struct {
		Network, Hash string
		Result        json.RawMessage
	}
	if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Network != network || body.Hash != hash || len(body.Result) == 0 || body.Result[0] != '{' {
		return nil, errors.New("invalid transaction lookup response")
	}
	return body.Result, nil
}
