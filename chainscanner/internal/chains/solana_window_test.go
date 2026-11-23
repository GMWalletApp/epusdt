package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestFinalizedSlotListRejectsInvalidResponsesAndRespectsTip(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	server := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "getGenesisHash":
			return "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", nil
		case "getSlot":
			return 12, nil
		case "getFirstAvailableBlock":
			return 11, nil
		case "getBlocks":
			var end int
			if err := json.Unmarshal(r.Params[1], &end); err != nil || end != 12 {
				t.Errorf("end = %d, err = %v", end, err)
			}
			switch calls.Add(1) {
			case 1:
				return []int{9}, nil
			case 2:
				return []int{13}, nil
			case 3:
				return []int{10, 10}, nil
			case 4:
				return []int{12}, nil
			default:
				return []int{10, 12}, nil
			}
		case "getBlock":
			return map[string]any{"blockhash": "hash", "transactions": []any{}}, nil
		}
		return nil, fmt.Errorf("unexpected %s", r.Method)
	})
	defer server.Close()
	src, err := NewSource(ctx, Network{Name: "solana", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Head(ctx); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, _, err := src.Read(ctx, 10, 10); err == nil {
			t.Fatal("accepted invalid or pruned list")
		}
	}
	for h := int64(10); h <= 12; h++ {
		if end, _, err := src.Read(ctx, h, h); err != nil || end != h {
			t.Fatal(h, end, err)
		}
	}
	if calls.Load() != 5 {
		t.Fatal("cache misses", calls.Load())
	}
}
