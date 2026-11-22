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
	v, _, err := c.query(ctx, network, hash, false, "")
	return v, err
}
func (c *strictClient) round(ctx context.Context, network, hash, previous string) (json.RawMessage, string, error) {
	return c.query(ctx, network, hash, true, previous)
}
func (c *strictClient) query(ctx context.Context, network, hash string, single bool, previous string) (json.RawMessage, string, error) {
	if !scanner.Supported(network) || hash == "" || strings.ContainsAny(hash, "/?#") {
		return nil, "", errors.New("invalid transaction lookup")
	}
	target := strings.TrimRight(c.client.URL, "/") + "/v1/transactions/" + url.PathEscape(network) + "/" + url.PathEscape(hash) + "/strict"
	if single {
		target += "?rounds=1"
	} else {
		target += "?manual=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.client.Token)
	if single && previous != "" {
		req.Header.Set("X-ChainScanner-Exclude", previous)
	}
	res, err := c.transport.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	pair := res.Header.Get("X-ChainScanner-Pair")
	if single && res.Header.Get("X-ChainScanner-Strict-Rounds") != "1" {
		if res.StatusCode == http.StatusOK {
			return nil, "", service.ErrStrictRoundUnsupported
		}
		return nil, "", fmt.Errorf("single-round lookup unavailable: HTTP %d", res.StatusCode)
	}
	if res.StatusCode != 200 {
		return nil, pair, fmt.Errorf("ChainScanner strict lookup HTTP %d", res.StatusCode)
	}
	var body struct {
		Network, Hash string
		Result        json.RawMessage
	}
	if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, pair, err
	}
	if body.Network != network || body.Hash != hash || len(body.Result) == 0 || body.Result[0] != '{' {
		return nil, pair, errors.New("invalid strict transaction response")
	}
	return body.Result, pair, nil
}
