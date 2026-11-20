package task

import (
	"sync"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

type ListenSolJob struct{}

var gListenSolJobLock sync.Mutex
var gSolanaBlockHeightLock = make(chan struct{}, 1)

func (r ListenSolJob) Run() {
	shouldRecordHeight := false
	func() {
		gListenSolJobLock.Lock()
		defer gListenSolJobLock.Unlock()

		log.Sugar.Debug("[ListenSolJob] Job triggered")
		if !data.IsChainEnabled(mdb.NetworkSolana) {
			log.Sugar.Debug("[ListenSolJob] chain disabled, skipping")
			return
		}
		shouldRecordHeight = true

		walletAddress, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkSolana)
		if err != nil {
			log.Sugar.Errorf("[ListenSolJob] Failed to get wallet addresses: %v", err)
			return
		}
		if len(walletAddress) <= 0 {
			log.Sugar.Debug("[ListenSolJob] No available wallet addresses")
			return
		}
		log.Sugar.Infof("[ListenSolJob] Found %d wallet addresses to monitor", len(walletAddress))
		var wg sync.WaitGroup
		for _, address := range walletAddress {
			log.Sugar.Infof("[ListenSolJob] Listening to address: %s", address.Address)
			wg.Add(1)
			go service.SolCallBack(address.Address, &wg)
		}
		wg.Wait()
		log.Sugar.Debug("[ListenSolJob] Job completed")
	}()

	if shouldRecordHeight {
		recordSolanaLatestBlockHeight()
	}
}

func recordSolanaLatestBlockHeight() {
	select {
	case gSolanaBlockHeightLock <- struct{}{}:
		defer func() { <-gSolanaBlockHeightLock }()
	default:
		log.Sugar.Debug("[ListenSolJob] previous Solana block height stats still running, skipping")
		return
	}

	height, err := service.SolGetBlockHeight()
	if err != nil {
		log.Sugar.Warnf("[ListenSolJob] get Solana block height failed: %v", err)
	} else {
		data.RecordRpcBlockHeight(mdb.NetworkSolana, height)
	}
}
