package feed

import (
	"github.com/GMWalletApp/ChainScanner/internal/chains"

	"errors"
)

type Watch struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type Delivery struct {
	ID       int64           `json:"id"`
	Activity chains.Activity `json:"activity"`
}

var ErrCursorNotReady = errors.New("network cursor has not initialized")

type Registration struct {
	Watch
	FromHeight int64 `json:"from_height"`
}
