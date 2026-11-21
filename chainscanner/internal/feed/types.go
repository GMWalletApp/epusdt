package feed

import (
	"errors"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

type Watch struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type Delivery struct {
	ID       int64           `json:"id"`
	Activity chains.Activity `json:"activity"`
}

var ErrCursorNotReady = errors.New("subscription boundary is unavailable")

type Registration struct {
	Watch
	FromHeight int64 `json:"from_height"`
}

// Both endpoints are inclusive. A termination fixes coverage immediately;
// pending confirmed reads can still enqueue deliveries through ToHeight.
type Termination struct {
	Registration
	ToHeight  int64 `json:"to_height"`
	Completed bool  `json:"completed"`
}
type SubscriptionReceipt struct {
	Registrations []Registration `json:"registrations"`
	Terminations  []Termination  `json:"terminations"`
}
