package chains

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Keep catch-up requests small. A provider that rejects parallel reads or
// truncates the first page gets a minute of ordinary single-part reads.
type pairedReads struct{ serialUntil atomic.Int64 }

type historyRead struct {
	end    int64
	events []Activity
}

func (p *pairedReads) enabled() bool { return time.Now().UnixNano() >= p.serialUntil.Load() }

func (p *pairedReads) read(ctx context.Context, start, second, end int64, read func(context.Context, int64, int64) (int64, []Activity, error)) (int64, []Activity, error) {
	var parts [2]historyRead
	group, call := errgroup.WithContext(ctx)
	for i, span := range [2]Span{{First: start, Last: second - 1}, {First: second, Last: end}} {
		group.Go(func() error {
			last, events, err := read(call, span.First, span.Last)
			parts[i] = historyRead{last, events}
			return err
		})
	}
	err := group.Wait()
	if err != nil || parts[0].end != second-1 {
		if ctx.Err() == nil {
			p.serialUntil.Store(time.Now().Add(time.Minute).UnixNano())
		}
		if err != nil {
			return 0, nil, err
		}
		// The next reservation resumes at the actual first-page boundary.
		return parts[0].end, parts[0].events, nil
	}
	return parts[1].end, append(parts[0].events, parts[1].events...), nil
}

// Repeated program IDs and Move addresses are common within a block or page.
// Keep evidence order and duplicates intact for callers of Source.Read.
type addressCache map[string]string

func (c addressCache) add(out *[]string, network, raw string) {
	value, ok := c[raw]
	if !ok {
		value, _ = Canonical(network, raw)
		if len(c) < 4096 && len(raw) <= 128 {
			c[raw] = value
		}
	}
	if value != "" {
		*out = append(*out, value)
	}
}
