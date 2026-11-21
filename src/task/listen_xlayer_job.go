package task

import (
	"sync"

	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

// XLayerPollJob polls the OKX Onchain OS Payment query API for pending
// X Layer provider orders. The provider offers no webhook, so polling is
// the only way to learn that an order settled. It runs frequently because
// the cashier polls the local check-status endpoint while the buyer pays;
// the settle itself is idempotent (OrderProcessing dedupes on the
// block_transaction_id).
type XLayerPollJob struct{}

var gXLayerPollJobLock sync.Mutex

func (r XLayerPollJob) Run() {
	gXLayerPollJobLock.Lock()
	defer gXLayerPollJobLock.Unlock()

	log.Sugar.Debug("[XLayerPollJob] Job triggered")
	service.PollXLayerProviderOrders()
}
