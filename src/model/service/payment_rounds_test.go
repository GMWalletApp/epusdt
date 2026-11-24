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

func TestTransientFailuresRetryUntilDefinitiveMissingSuspendsParentAndChild(t *testing.T) {
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
	read := func(_ context.Context, n, h string) (json.RawMessage, error) {
		calls++
		if calls <= 3 {
			return nil, errors.New("RPC unavailable")
		}
		return nil, ErrTransactionNotFound
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if ok, err := processDueConfirmation(ctx, nil, read); !ok || err == nil {
			t.Fatal(ok, err)
		}
		var job mdb.PaymentConfirmation
		if err := db.Take(&job).Error; err != nil || job.Attempts != attempt {
			t.Fatal(job, err)
		}
		o, err := data.GetOrderInfoByTradeId("child")
		if err != nil || o.Status != mdb.StatusConfirming || o.BlockTransactionId != "" {
			t.Fatal(o, err)
		}
		if err := db.Model(&job).Update("retry_at", time.Now().Add(-time.Second).UnixMilli()).Error; err != nil {
			t.Fatal(err)
		}
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
	if ok, err := processDueConfirmation(ctx, nil, read); ok || err != nil || calls != 4 {
		t.Fatal(ok, err, calls)
	}
	if err := queueConfirmation(child, e); !errors.Is(err, errInactiveCandidate) {
		t.Fatal("duplicate event revived review", err)
	}
	if !manualPaymentStatusAllowed(mdb.StatusManualReview, adminManualPaymentStatuses) || manualPaymentStatusAllowed(mdb.StatusManualReview, manualPaymentWaitPayStatuses) {
		t.Fatal("manual review permissions")
	}
}

func TestLegacyJobWithTwoAttemptsResumesLookup(t *testing.T) {
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
	called := false
	read := func(context.Context, string, string) (json.RawMessage, error) {
		called = true
		return nil, errors.New("still unavailable")
	}
	if ok, err := processDueConfirmation(t.Context(), nil, read); !ok || err == nil || !called {
		t.Fatal(ok, err, called)
	}
	got, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil || got.Status != mdb.StatusConfirming {
		t.Fatal(got, err)
	}
}

func TestVerificationPolicySurvivesSettingChangeAndReplay(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	o := mdb.Orders{TradeId: "settings", OrderId: "settings", Status: mdb.StatusWaitPay}
	if err := dao.Mdb.Create(&o).Error; err != nil {
		t.Fatal(err)
	}
	first := scanner.Activity{TxID: "first"}
	if err := queueConfirmation(&o, first); err != nil {
		t.Fatal(err)
	}
	if err := data.SetSetting("system", mdb.SettingKeyStrictPayment, "false", mdb.SettingTypeBool); err != nil {
		t.Fatal(err)
	}
	if err := queueConfirmation(&o, first); err != nil {
		t.Fatal(err)
	}
	if err := queueConfirmation(&o, scanner.Activity{TxID: "second"}); err != nil {
		t.Fatal(err)
	}
	var jobs []mdb.PaymentConfirmation
	if err := dao.Mdb.Order("id").Find(&jobs).Error; err != nil || len(jobs) != 2 || jobs[0].SkipStrict || !jobs[1].SkipStrict {
		t.Fatal(jobs, err)
	}
}

type staticHeightFeed struct {
	scanner.Feed
	height int64
}

func (f staticHeightFeed) Height(context.Context, string) (int64, error) {
	return f.height, nil
}

func TestConfirmingOrderRenewsLockAndWaitingBlockDepthReleasesLease(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(&mdb.Chain{}).Where("network = ?", mdb.NetworkEthereum).Update("min_confirmations", 3).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	req := newCreateTransactionRequest("confirming-lock-order", 10)
	req.Network = mdb.NetworkEthereum
	req.Token = "USDT"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(2 * time.Second)
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", resp.TradeId).UpdateColumn("expires_at", soon).Error; err != nil {
		t.Fatal(err)
	}
	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	e := scanner.Activity{
		Network:       mdb.NetworkEthereum,
		TxID:          "0x" + "1111111111111111111111111111111111111111111111111111111111111111",
		Height:        10,
		Confirmations: 1,
	}
	if err := queueConfirmation(order, e); err != nil {
		t.Fatal(err)
	}
	var lock mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", resp.TradeId).Take(&lock).Error; err != nil {
		t.Fatal(err)
	}
	if !lock.ExpiresAt.After(soon.Add(30 * time.Second)) {
		t.Fatalf("confirming order lock was not renewed: expires_at=%v", lock.ExpiresAt)
	}
	lookupCalls := 0
	ok, err := processDueConfirmation(t.Context(), staticHeightFeed{height: 10}, func(context.Context, string, string) (json.RawMessage, error) {
		lookupCalls++
		return nil, nil
	})
	if !ok || !errors.Is(err, errWaitingBlockDepth) || lookupCalls != 0 {
		t.Fatalf("ok=%v err=%v lookupCalls=%d", ok, err, lookupCalls)
	}
	var job mdb.PaymentConfirmation
	if err := dao.Mdb.Where("order_id = ?", order.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Attempts != 0 {
		t.Fatalf("waiting for block depth consumed attempts: %d", job.Attempts)
	}
	if job.RetryAt > time.Now().Add(5*time.Second).UnixMilli() {
		t.Fatalf("waiting for block depth left 60s lease: retry_at=%d", job.RetryAt)
	}
}
