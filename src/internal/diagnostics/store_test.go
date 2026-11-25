package diagnostics

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetentionToggleRestartAndOrderExport(t *testing.T) {
	dir := t.TempDir()
	var enabled atomic.Bool
	enabled.Store(true)
	start := func() func() {
		stop, err := Start(context.Background(), dir, enabled.Load)
		if err != nil {
			t.Fatal(err)
		}
		return stop
	}
	stop := start()
	now := time.Now().UTC()
	s := active.Load()
	for _, e := range []Event{
		{Time: now.Add(-retention), Event: "expired", Hash: "HASH"},
		{Time: now.Add(-retention + time.Hour), Event: "scan.observed", Hash: "HASH"},
		{Time: now, Event: "order.matched", Hash: "HASH", Trade: "TRADE", MerchantOrder: "MERCHANT"},
		{Time: now, Event: "order.create", Trade: "TRADE", MerchantOrder: "MERCHANT"},
		{Time: now, Event: "other", Trade: "OTHER"},
	} {
		if err := s.write(e); err != nil {
			t.Fatal(err)
		}
	}
	export := func(ref string) string {
		var b bytes.Buffer
		if err := Export(t.Context(), &b, ref); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	for _, ref := range []string{"TRADE", "MERCHANT", "HASH"} {
		logs := export(ref)
		for _, want := range []string{"scan.observed", "order.matched", "order.create"} {
			if !strings.Contains(logs, want) {
				t.Fatalf("%s missing %s: %s", ref, want, logs)
			}
		}
		if strings.Contains(logs, "expired") || strings.Contains(logs, "other") {
			t.Fatal(logs)
		}
	}
	Record(Event{Event: "enabled"})
	enabled.Store(false)
	Record(Event{Event: "disabled"})
	stop()
	stop = start()
	defer stop()
	all := export("")
	if !strings.Contains(all, "enabled") || strings.Contains(all, "disabled") || !strings.Contains(all, "other") {
		t.Fatal(all)
	}
	var remaining int
	if err := active.Load().db.QueryRow("SELECT count(*) FROM events WHERE at<=?", now.Add(-retention).UnixMilli()).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal(remaining, err)
	}
	if err := active.Load().prune(now.Add(25 * time.Hour)); err != nil || export("") != "" {
		t.Fatal("cleanup while recording disabled", err)
	}
}
