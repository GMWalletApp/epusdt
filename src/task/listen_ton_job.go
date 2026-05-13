package task

import (
	"sync"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

type ListenTonJob struct{}

var gListenTonJobLock sync.Mutex

func (r ListenTonJob) Run() {
	gListenTonJobLock.Lock()
	defer gListenTonJobLock.Unlock()
	log.Sugar.Debug("[ListenTonJob] Job triggered")
	if !data.IsChainEnabled(mdb.NetworkTon) {
		log.Sugar.Debug("[ListenTonJob] chain disabled, skipping")
		return
	}
	walletAddresses, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkTon)
	if err != nil {
		log.Sugar.Errorf("[ListenTonJob] failed to get wallet addresses: %v", err)
		return
	}
	if len(walletAddresses) == 0 {
		log.Sugar.Debug("[ListenTonJob] no available wallet addresses")
		return
	}
	log.Sugar.Infof("[ListenTonJob] scanning %d wallet addresses", len(walletAddresses))
	var wg sync.WaitGroup
	for _, addr := range walletAddresses {
		wg.Add(1)
		go service.TonCallBack(addr.Address, &wg)
	}
	wg.Wait()
	log.Sugar.Debug("[ListenTonJob] job completed")
}
