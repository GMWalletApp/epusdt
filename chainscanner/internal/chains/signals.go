package chains

import (
	"context"
	"time"
)

// Wakeups coalesces tip notifications. The persisted cursor determines the next
// read even when a stream drops notifications or reconnects after an outage.
func Wakeups(s Source) <-chan struct{} {
	if v, ok := s.(interface{ Wakeups() <-chan struct{} }); ok {
		return v.Wakeups()
	}
	if v, ok := s.(interface{ wakeups() <-chan struct{} }); ok {
		return v.wakeups()
	}
	return nil
}

type headSignals struct {
	ch   chan struct{}
	stop context.CancelFunc
}

func startSignals(parent context.Context, stream func(context.Context, func()) error) *headSignals {
	ctx, cancel := context.WithCancel(parent)
	s := &headSignals{make(chan struct{}, 1), cancel}
	go func() {
		for ctx.Err() == nil {
			_ = stream(ctx, func() {
				select {
				case s.ch <- struct{}{}:
				default:
				}
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return s
}
func (s *jsonSource) wakeups() <-chan struct{} {
	if s.heads == nil {
		return nil
	}
	return s.heads.ch
}
