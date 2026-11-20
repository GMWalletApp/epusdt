package nodes

import "testing"

func BenchmarkRecordLookup(b *testing.B) {
	var lookup transactionLookup
	for i := range 2048 {
		lookup.record(nodeVersion{id: int64(i)}, nil)
	}
	target := nodeVersion{id: 123}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		lookup.record(target, nil)
	}
}
