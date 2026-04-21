package service

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/assimon/luuu/internal/testutil"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/tidwall/gjson"
)

func TestWalkTronGridPagesFollowsFingerprint(t *testing.T) {
	var calls int
	var ids []int64
	err := walkTronGridPages(func(query map[string]string) ([]byte, error) {
		calls++
		switch calls {
		case 1:
			if query["fingerprint"] != "" {
				t.Fatalf("unexpected first fingerprint: %q", query["fingerprint"])
			}
			return []byte(`{"data":[{"id":1},{"id":2}],"meta":{"fingerprint":"next-page"}}`), nil
		case 2:
			if query["fingerprint"] != "next-page" {
				t.Fatalf("unexpected second fingerprint: %q", query["fingerprint"])
			}
			return []byte(`{"data":[{"id":3}],"meta":{}}`), nil
		default:
			return nil, fmt.Errorf("unexpected extra call %d", calls)
		}
	}, map[string]string{"limit": "100"}, func(record gjson.Result) bool {
		ids = append(ids, record.Get("id").Int())
		return true
	})
	if err != nil {
		t.Fatalf("walkTronGridPages returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 page fetches, got %d", calls)
	}
	if got := fmt.Sprint(ids); got != "[1 2 3]" {
		t.Fatalf("unexpected collected ids %s", got)
	}
}

func TestTryProcessEvmERC20TransferSkipsHistoricalTransfer(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	order := &mdb.Orders{
		TradeId:        "evm-history-order",
		OrderId:        "evm-history-order",
		Amount:         1.00,
		Currency:       "USD",
		ActualAmount:   1.00,
		ReceiveAddress: "0x08c34c4e8b99e2503017ae09287bd0019b7096c6",
		Token:          "USDT",
		Network:        mdb.NetworkEthereum,
		Status:         mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create evm order: %v", err)
	}
	if err := data.LockTransaction(order.Network, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock evm order: %v", err)
	}

	TryProcessEvmERC20Transfer(
		mdb.NetworkEthereum,
		common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"),
		common.HexToAddress(order.ReceiveAddress),
		big.NewInt(1_000_000),
		"0xhistorical",
		order.CreatedAt.TimestampMilli()-1000,
	)

	current, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatalf("reload evm order: %v", err)
	}
	if current.Status != mdb.StatusWaitPay {
		t.Fatalf("historical transfer should not mark order paid: %+v", current)
	}
	if current.BlockTransactionId != "" {
		t.Fatalf("historical transfer should not set block transaction id: %+v", current)
	}
}
