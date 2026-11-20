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
func strictLookup(client scanner.Client, local http.Handler) service.StrictLookup {
	transport := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, network, hash string) (json.RawMessage, error) {
		if !scanner.Supported(network) || hash == "" || strings.ContainsAny(hash, "/?#") {
			return nil, errors.New("invalid transaction lookup")
		}
		target := strings.TrimRight(client.URL, "/") + "/v1/transactions/" + url.PathEscape(network) + "/" + url.PathEscape(hash) + "/strict"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+client.Token)
		var res *http.Response
		if local != nil {
			w := httptest.NewRecorder()
			local.ServeHTTP(w, req)
			res = w.Result()
		} else {
			res, err = transport.Do(req)
			if err != nil {
				return nil, err
			}
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("ChainScanner strict lookup HTTP %d", res.StatusCode)
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
		return body.Result, nil
	}
}

func embeddedStrictLookup(ctx context.Context, store *scanner.Store) (service.StrictLookup, error) {
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
	return strictLookup(scanner.Client{Token: token}, handler), nil
}
