package task

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/service"
)

// Embedded and remote deployments invoke the same authenticated /strict route.
// In embedded mode the handler runs in-process without opening a listening port.
type strictClient struct {
	client    scanner.Client
	local     http.Handler
	transport *http.Client
}

func newStrictClient(client scanner.Client, local http.Handler) *strictClient {
	transport := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &strictClient{client, local, transport}
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
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.client.Token)
	if single && previous != "" {
		req.Header.Set("X-ChainScanner-Exclude", previous)
	}
	var res *http.Response
	if c.local != nil {
		w := httptest.NewRecorder()
		c.local.ServeHTTP(w, req)
		res = w.Result()
	} else {
		res, err = c.transport.Do(req)
		if err != nil {
			return nil, "", err
		}
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

func embeddedStrictLookup(ctx context.Context, store *scanner.Store) (*strictClient, error) {
	var networks []scanner.Network
	for _, n := range scannerNetworks {
		networks = append(networks, scanner.Network{Name: n})
	}
	registry, err := scanner.NewNodeRegistry(ctx, store, networks)
	if err != nil {
		return nil, err
	}
	token := rand.Text()
	handler, err := registry.Handler(map[string]string{paymentConsumer: token}, "")
	if err != nil {
		return nil, err
	}
	go registry.RunChainList(ctx)
	return newStrictClient(scanner.Client{Token: token}, handler), nil
}
