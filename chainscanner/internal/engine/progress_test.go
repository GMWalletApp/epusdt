package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

func TestLagFollowsOldestOutstandingObservedTarget(t *testing.T) {
	start := time.Unix(1000, 0)
	var samples headHistory
	for i := 0; i <= 72; i++ {
		samples.record(1000+int64(i)*50, start.Add(time.Duration(i)*5*time.Second))
	}
	now := start.Add(6 * time.Minute)
	for _, tc := range []struct {
		cursor int64
		lag    time.Duration
		status string
	}{
		{1499, 310 * time.Second, "warning"}, {1599, 300 * time.Second, "ok"}, {4600, 0, "ok"},
	} {
		p := samples.progress("solana", tc.cursor, now, lagWarning)
		if p.Status != tc.status || p.LagMS == nil || *p.LagMS != tc.lag.Milliseconds() {
			t.Fatalf("cursor %d, got %+v", tc.cursor, p)
		}
	}
	if p := samples.progress("solana", 4600, now.Add(6*time.Minute), lagWarning); p.Status != "stale" || p.LagMS != nil {
		t.Fatalf("stale head: %+v", p)
	}
	samples.record(1400, now.Add(time.Second))
	if p := samples.progress("solana", 1400, now.Add(time.Second), lagWarning); p.Status != "ok" || *p.LagMS != 0 {
		t.Fatalf("revised head: %+v", p)
	}
	for i := 1; i <= 2000; i++ {
		samples.record(1400+int64(i), now.Add(time.Duration(i)*sampleInterval))
	}
	if len(samples.samples) > maxSamples {
		t.Fatal("unbounded sample history")
	}
}

func TestProgressUsesContiguousCursorAndResetsWithLastWatch(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	ConfigureNetworks(s, map[string]chains.Network{"base": {Name: "base"}}, nil)
	get := func() ScanProgress {
		t.Helper()
		rows, err := s.ScanProgress(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	if p := get(); p.Status != "idle" || p.LagMS != nil {
		t.Fatal(p)
	}
	s.setDemandLocked("base", true)
	if p := get(); p.Status != "warming" || p.Cursor != nil {
		t.Fatal(p)
	}
	start := time.Now().Add(-7 * time.Minute)
	s.periods["base"].started = start
	if p := get(); p.Status != "stale" {
		t.Fatal(p)
	}
	if err := s.SeedCursor(ctx, "base", 200); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO scan_gaps(network,first,last) VALUES('base',101,102)"); err != nil {
		t.Fatal(err)
	}
	s.RecordScanHead(ctx, "base", 150, start)
	s.RecordScanHead(ctx, "base", 201, time.Now())
	if p := get(); p.Status != "warning" || *p.Cursor != 100 || *p.Target != 201 {
		t.Fatal(p)
	}
	s.setDemandLocked("base", false)
	if p := get(); p.Status != "idle" || p.Target != nil {
		t.Fatal(p)
	}
	s.setDemandLocked("base", true)
	s.RecordScanHead(ctx, "base", 999, start)
	if p := get(); p.Status != "warming" || p.Target != nil {
		t.Fatal(p)
	}
}

func TestRangeCountsExcludeHeadPollsCancellationAndUnwatchedNetworks(t *testing.T) {
	ctx := t.Context()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := chains.Network{Name: "base"}
	ConfigureNetworks(s, map[string]chains.Network{"base": base, "ethereum": {Name: "ethereum"}}, func(context.Context, string) (int64, error) { return 19, nil })
	watches := []feed.Watch{{Network: "base", Address: wallet}}
	if _, err := s.Subscribe(ctx, "pay", watches); err != nil {
		t.Fatal(err)
	}
	check := func(want uint64) {
		t.Helper()
		rows, err := s.ScanProgress(ctx)
		if err != nil || len(rows) != 2 {
			t.Fatal(rows, err)
		}
		for _, r := range rows {
			if r.ScanFailureCount != 0 || r.Network == "base" && r.ScanSuccessCount != want || r.Network == "ethereum" && r.ScanSuccessCount != 0 {
				t.Fatalf("unexpected range counters: %+v", r)
			}
		}
	}
	if _, err := s.Step(ctx, base, &chain{height: 19}); err != nil {
		t.Fatal(err)
	}
	check(0)
	inFlight := &lateSource{entered: make(chan struct{}), release: make(chan struct{})}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Step(run, base, inFlight); done <- err }()
	<-inFlight.entered
	cancel()
	close(inFlight.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check(0)
	if _, err := s.Step(ctx, base, &chain{height: 20}); err != nil {
		t.Fatal(err)
	}
	check(1)
	if _, err := s.Subscribe(ctx, "pay", nil); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := s.Subscribe(ctx, "pay", watches); err != nil {
		t.Fatal(err)
	}
	check(0)
}
