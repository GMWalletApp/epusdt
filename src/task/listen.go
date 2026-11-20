package task

import (
	"context"
	"os"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/robfig/cron/v3"
)

func Start() {
	ctx := context.Background()
	var store *scanner.Store
	var feed scanner.Feed
	if url := os.Getenv("CHAINSCANNER_URL"); url != "" {
		feed = scanner.Client{URL: url, Token: os.Getenv("CHAINSCANNER_TOKEN")}
	} else {
		var err error
		store, err = scanner.Open(config.GetRuntimeSqlitePath() + ".chainscanner")
		if err != nil {
			panic(err)
		}
		configureEmbedded(store)
		feed = store
	}
	subscriptions := &paymentSubscriptions{feed: feed}
	service.EnsurePaymentWatch = subscriptions.ensure
	go func() {
		if store != nil {
			for _, network := range scannerNetworks {
				go runEmbeddedNetwork(ctx, network, store)
			}
		}
		go consumeScanner(ctx, feed)
		for {
			if err := subscriptions.refresh(ctx); err != nil {
				log.Sugar.Errorf("[ChainScanner] subscribe: %v", err)
			}
			time.Sleep(5 * time.Second)
		}
	}()
	if store != nil {
		c := cron.New()
		if _, err := c.AddJob("@every 30s", RpcHealthJob{watching: store.Watching}); err != nil {
			panic(err)
		}
		c.Start()
	}
	if store == nil {
		go func() {
			for range time.Tick(15 * time.Second) {
				for _, n := range scannerNetworks {
					if h, e := feed.Height(ctx, n); e == nil && h >= 0 {
						data.RecordRpcBlockHeight(n, h)
					}
				}
			}
		}()
	}
}
