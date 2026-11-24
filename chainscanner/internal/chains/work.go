package chains

import (
	"context"
	"errors"
)

// Span is an inclusive range of block heights (ledger versions on Aptos).
type Span struct {
	First, Last int64
	Failed      bool // a previous scan attempt for this range failed
}

type FinishRange = func(Span, BlockLink, []Activity, error) error

// ErrFork marks a provider response incompatible with an already accepted neighbor.
var ErrFork = errors.New("block ancestry conflicts with a completed neighbor")

// ReadSpan returns one complete adapter response, including the exact boundary
// of a partial Aptos page. A linked EVM source must identify both neighbors.
func ReadSpan(ctx context.Context, network string, s Source, r Span) (Span, BlockLink, []Activity, error) {
	var link BlockLink
	var rows []Activity
	var end int64
	var err error
	if reader, ok := s.(interface {
		ReadLinkedBlock(context.Context, int64, *BlockLink) (int64, []Activity, error)
	}); ok && (IsEVM(network) || network == "bitcoin") {
		end, rows, err = reader.ReadLinkedBlock(ctx, r.First, &link)
		if err == nil && (link.Hash == "" || (r.First > 0 && link.Parent == "") || end != r.First) {
			err = errors.New("invalid block link")
		}
	} else {
		end, rows, err = s.Read(ctx, r.First, r.Last)
	}
	if err != nil {
		return r, link, nil, err
	}
	if end < r.First || end > r.Last {
		return r, link, nil, errors.New("invalid node range")
	}
	r.Last = end
	for _, e := range rows {
		if e.Height < r.First || e.Height > end || (e.Network != "" && e.Network != network) {
			return r, link, nil, errors.New("event outside assigned network or range")
		}
	}
	return r, link, rows, nil
}
