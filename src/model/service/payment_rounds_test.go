package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestStrictRoundsPersistAcrossWorkersAndStopForReview(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	db := dao.Mdb
	ctx := t.Context()
	for _, id := range []string{"parent", "child"} {
		o := mdb.Orders{TradeId: id, OrderId: id, Token: "ETH", Network: "ethereum", Status: mdb.StatusConfirming, ReceiveAddress: "0x1111111111111111111111111111111111111111"}
		if id == "child" {
			o.ParentTradeId = "parent"
		}
		if err := db.Create(&o).Error; err != nil {
			t.Fatal(err)
		}
	}
	child, err := data.GetOrderInfoByTradeId("child")
	if err != nil {
		t.Fatal(err)
	}
	e := scanner.Activity{Network: "ethereum", TxID: "candidate", Confirmations: 10000}
	if err := queueConfirmation(child, e); err != nil {
		t.Fatal(err)
	}
	calls := 0
	read := func(_ context.Context, n, h, previous string) (json.RawMessage, string, error) {
		calls++
		if calls == 1 && previous != "" || calls == 2 && previous != "first-pair" {
			t.Fatal(calls, previous)
		}
		return nil, "first-pair", errors.New("no quorum")
	}
	if ok, err := processDueConfirmation(ctx, nil, read); !ok || err == nil {
		t.Fatal(ok, err)
	}
	var job mdb.PaymentConfirmation
	if err := db.Take(&job).Error; err != nil || job.Attempts != 1 || job.PreviousPair != "first-pair" {
		t.Fatal(job, err)
	}
	if err := db.Model(&job).Update("retry_at", time.Now().Add(-time.Second).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if ok, err := processDueConfirmation(ctx, nil, read); !ok || err != nil {
		t.Fatal(ok, err)
	}
	for _, id := range []string{"parent", "child"} {
		o, err := data.GetOrderInfoByTradeId(id)
		if err != nil || o.Status != mdb.StatusManualReview || o.ReviewTransactionHash != e.TxID || o.BlockTransactionId != "" {
			t.Fatal(o, err)
		}
	}
	if ok, err := processDueConfirmation(ctx, nil, read); ok || err != nil || calls != 2 {
		t.Fatal(ok, err, calls)
	}
	if err := queueConfirmation(child, e); !errors.Is(err, errInactiveCandidate) {
		t.Fatal("duplicate event revived review", err)
	}
	if !manualPaymentStatusAllowed(mdb.StatusManualReview, adminManualPaymentStatuses) || manualPaymentStatusAllowed(mdb.StatusManualReview, manualPaymentWaitPayStatuses) {
		t.Fatal("manual review permissions")
	}
}

func TestCrashAfterReservingLastStrictRoundRequiresReview(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	db := dao.Mdb
	order := mdb.Orders{TradeId: "crash", OrderId: "crash", Network: "ethereum", Status: mdb.StatusConfirming}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	e := scanner.Activity{Network: order.Network, TxID: "reserved", Confirmations: 10000}
	if err := queueConfirmation(&order, e); err != nil {
		t.Fatal(err)
	}
	// The second worker died after reserving its round, before recording a response.
	if err := db.Model(&mdb.PaymentConfirmation{}).Where("order_id = ?", order.ID).Updates(map[string]any{"attempts": 2, "retry_at": time.Now().Add(-time.Minute).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	unexpected := func(context.Context, string, string, string) (json.RawMessage, string, error) {
		t.Fatal("third round started after restart")
		return nil, "", nil
	}
	if ok, err := processDueConfirmation(t.Context(), nil, unexpected); !ok || err != nil {
		t.Fatal(ok, err)
	}
	got, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil || got.Status != mdb.StatusManualReview || got.ReviewTransactionHash != e.TxID {
		t.Fatal(got, err)
	}
}
