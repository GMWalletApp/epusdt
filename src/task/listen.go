package task

import (
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/robfig/cron/v3"
)

func Start() {
	log.Sugar.Info("[task] Starting task scheduler...")
	// The ETH listener short-circuits internally when chain is disabled
	// or no tokens are configured, so always launch the goroutine.
	go StartEthereumWebSocketListener()
	go StartBscWebSocketListener()
	go StartPolygonWebSocketListener()
	go StartPlasmaWebSocketListener()
	go StartTronBlockScannerListener()

	c := cron.New()

	// Solana polling
	_, err := c.AddJob("@every 5s", ListenSolJob{})
	if err != nil {
		log.Sugar.Errorf("[task] Failed to add ListenSolJob: %v", err)
		return
	}

	log.Sugar.Info("[task] ListenSolJob scheduled successfully (@every 5s)")

	// TON polling — lite servers don't expose a transfer-event firehose,
	// so we walk each owner's transaction history every tick.
	_, err = c.AddJob("@every 5s", ListenTonJob{})
	if err != nil {
		log.Sugar.Errorf("[task] Failed to add ListenTonJob: %v", err)
		return
	}
	log.Sugar.Info("[task] ListenTonJob scheduled successfully (@every 5s)")

	// RPC node health checks
	_, err = c.AddJob("@every 30s", RpcHealthJob{})
	if err != nil {
		log.Sugar.Errorf("[task] Failed to add RpcHealthJob: %v", err)
		return
	}
	log.Sugar.Info("[task] RpcHealthJob scheduled successfully (@every 30s)")

	c.Start()
	log.Sugar.Info("[task] Task scheduler started")
}
