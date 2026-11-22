package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/ton"
)

// ConnectTonLiteAPI builds a tonutils-go API client from a direct ADNL peer or a global
// config URL. Scanner and manual verification use this same connection setup.
func ConnectTonLiteAPI(ctx context.Context, configURL string, requestTimeout time.Duration, reconnectLimit int) (ton.APIClientWrapped, func(), error) {
	configURL = strings.TrimSpace(configURL)
	if configURL == "" {
		return nil, func() {}, fmt.Errorf("ton lite config url is empty")
	}
	if reconnectLimit <= 0 {
		reconnectLimit = 3
	}
	pool := liteclient.NewConnectionPool()
	pool.SetOnDisconnect(pool.DefaultReconnect(3*time.Second, reconnectLimit))
	base := ton.NewAPIClient(pool, ton.ProofCheckPolicyFast)
	var err error
	if strings.HasPrefix(configURL, "adnl://") {
		err = scanner.ConnectTONPeer(ctx, pool, configURL)
	} else {
		var cfg *liteclient.GlobalConfig
		cfg, err = liteclient.GetConfigFromUrl(ctx, configURL)
		if err == nil {
			base.SetTrustedBlockFromConfig(cfg)
			err = pool.AddConnectionsFromConfig(ctx, cfg)
		}
	}
	if err != nil {
		pool.Stop()
		return nil, func() {}, err
	}
	api := base.WithRetry(2).WithTimeout(requestTimeout).WithLSInfoInErrors()
	return api, pool.Stop, nil
}
