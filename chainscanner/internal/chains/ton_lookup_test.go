package chains

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
)

type wrongTONTransaction struct {
	ton.APIClientWrapped
	block *ton.BlockIDExt
	tx    *tlb.Transaction
}

func (p wrongTONTransaction) LookupBlock(context.Context, int32, int64, uint32) (*ton.BlockIDExt, error) {
	return p.block, nil
}
func (p wrongTONTransaction) GetTransaction(context.Context, *ton.BlockIDExt, *address.Address, uint64) (*tlb.Transaction, error) {
	return p.tx, nil
}

func TestTONLocatorCannotSupplyAnUnprovenTransaction(t *testing.T) {
	a := address.NewAddress(0, 255, bytes.Repeat([]byte{42}, 32))
	l := TONLocation{account: a, lt: 8, seq: 20, master: 20, shard: -1 << 63}
	p := wrongTONTransaction{block: &ton.BlockIDExt{Workchain: -1, Shard: -1 << 63, SeqNo: 20, RootHash: make([]byte, 32)}, tx: &tlb.Transaction{LT: 8, AccountAddr: a.Data(), Hash: bytes.Repeat([]byte{0xff}, 32)}}
	if v, err := verifyLocatedTON(t.Context(), p, strings.Repeat("00", 32), l); err == nil || len(v) != 0 {
		t.Fatal("accepted wrong transaction hash")
	}
	l.seq++
	if _, err := verifyLocatedTON(t.Context(), p, strings.Repeat("ff", 32), l); err == nil {
		t.Fatal("accepted wrong block coordinate")
	}
	if _, _, ok := TONVoter(Network{Name: "ton", Protocol: "lite", URL: "https://ton.org/global-config.json"}); ok {
		t.Fatal("shared peer list cast a vote")
	}
}
