package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/log"
)

const (
	moveScannerRetryDelay = 3 * time.Second
	moveCatchupYield      = 250 * time.Millisecond
)

type moveWatchState struct {
	wallets map[string]struct{}
	tokens  []mdb.ChainToken
}

func moveChainConfig(network string) (*mdb.Chain, time.Duration) {
	row, err := data.GetChainByNetwork(network)
	if err != nil || row == nil || row.ID == 0 {
		if err != nil {
			log.Sugar.Warnf("[%s] load chain config failed: %v", strings.ToUpper(network), err)
		}
		return nil, 5 * time.Second
	}
	interval := time.Duration(row.ScanIntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return row, interval
}

func loadMoveWatchState(network string) (moveWatchState, error) {
	rows, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return moveWatchState{}, err
	}
	state := moveWatchState{wallets: make(map[string]struct{})}
	for _, row := range rows {
		address, err := addressutil.NormalizeMoveAddress(row.Address)
		if err != nil {
			log.Sugar.Warnf("[%s] skip invalid wallet address=%s err=%v", strings.ToUpper(network), row.Address, err)
			continue
		}
		state.wallets[address] = struct{}{}
	}
	tokens, err := data.ListEnabledChainTokensByNetwork(network)
	if err != nil {
		return moveWatchState{}, err
	}
	state.tokens = tokens
	return state, nil
}

func moveWatchStateSignature(state moveWatchState) string {
	return fmt.Sprintf("wallets=%d/tokens=%d", len(state.wallets), len(state.tokens))
}

func moveConfirmedCursor(latest int64, minConfirmations int) int64 {
	if latest <= 0 {
		return 0
	}
	if minConfirmations <= 1 {
		return latest
	}
	confirmed := latest - int64(minConfirmations) + 1
	if confirmed < 0 {
		return 0
	}
	return confirmed
}
