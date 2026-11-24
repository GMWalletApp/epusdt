package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/diagnostics"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TransactionLookup func(context.Context, string, string) (json.RawMessage, error)

type ObservedTransactionLookup func(ctx context.Context, network, hash string, height int64) (json.RawMessage, error)

// Installed during startup for subscription and manual on-chain payment checks.
var LookupPaymentTransaction TransactionLookup

func queueConfirmation(order *mdb.Orders, e scanner.Activity) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	err = dao.Mdb.Transaction(func(tx *gorm.DB) error {
		// Always lock parent first so a direct payment and a child payment serialize.
		ids := []string{order.TradeId}
		if order.ParentTradeId != "" {
			ids = []string{order.ParentTradeId, order.TradeId}
		}
		for _, id := range ids {
			var row mdb.Orders
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_id = ?", id).Take(&row).Error; err != nil {
				return err
			}
			switch row.Status {
			case mdb.StatusWaitPay, mdb.StatusExpired, mdb.StatusConfirming:
			default:
				return errInactiveCandidate
			}
			if row.Status != mdb.StatusConfirming {
				res := tx.Model(&row).Where("status = ?", row.Status).Update("status", mdb.StatusConfirming)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return errInactiveCandidate
				}
			}
			if hasTransactionLock(&row) {
				if err := data.ExtendOrderTransactionLock(&row, time.Now().Add(config.GetOrderExpirationTimeDuration())); err != nil {
					return err
				}
			}
		}
		// Read the policy transactionally across payment replicas. Re-inclusion
		// or redelivery of the same hash keeps the original recheck setting.
		var previous mdb.PaymentConfirmation
		q := tx.Select("id", "skip_strict").Where("order_id = ? AND hash = ?", order.ID, e.TxID).Order("id").Limit(1).Find(&previous)
		if q.Error != nil {
			return q.Error
		}
		skipStrict := previous.SkipStrict
		if previous.ID == 0 {
			var setting mdb.Setting
			if err := tx.Select("value").Where(&mdb.Setting{Key: mdb.SettingKeyStrictPayment}).Limit(1).Find(&setting).Error; err != nil {
				return err
			}
			skipStrict = setting.Value == "false"
		}
		digest := sha256.Sum256(payload) // A re-included transaction may have new block evidence.
		job := mdb.PaymentConfirmation{SkipStrict: skipStrict, Position: hex.EncodeToString(digest[:]), OrderID: order.ID, Hash: e.TxID, Activity: string(payload), RetryAt: time.Now().UnixMilli()}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
	})
	if err == nil {
		diagnostics.Record(diagnostics.Event{Event: "order.matched", Order: order.ID, Trade: order.TradeId, Parent: order.ParentTradeId, MerchantOrder: order.OrderId, Network: e.Network, Hash: e.TxID, Height: e.Height, Confirmations: e.Confirmations, Token: order.Token, Amount: order.ActualAmount, Status: mdb.StatusConfirming})
	}
	return err
}

// ErrTransactionNotFound means the scanner completed a chain lookup for this hash.
var ErrTransactionNotFound = errors.New("transaction not found on chain")

var errPaymentMismatch = errors.New("scanned transfer does not match order")
var errWaitingBlockDepth = errors.New("waiting for required block depth")

var errInactiveCandidate = errors.New("payment candidate no longer active")

// Each worker claims one due transfer. A crash releases the claim at its deadline.
// Failed lookups leave other orders and subsequent feed deliveries free to proceed.
func RunPaymentConfirmations(ctx context.Context, feed scanner.Feed, lookup ObservedTransactionLookup) {
	for ctx.Err() == nil {
		worked, err := processDueConfirmation(ctx, feed, lookup)
		if err != nil && ctx.Err() == nil {
			log.Sugar.Warnf("[payment confirmation] %v", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func processDueConfirmation(ctx context.Context, feed scanner.Feed, lookup ObservedTransactionLookup) (bool, error) {
	db := dao.Mdb.WithContext(ctx)
	var job mdb.PaymentConfirmation
	now := time.Now()
	err := db.Where("retry_at <= ?", now.UnixMilli()).Order("retry_at, id").Limit(1).Find(&job).Error
	if err != nil || job.ID == 0 {
		return false, err
	}
	lease := now.Add(time.Minute).UnixMilli()
	claim := db.Model(&job).Where("retry_at = ?", job.RetryAt).Update("retry_at", lease)
	if claim.Error != nil {
		return false, claim.Error
	}
	if claim.RowsAffected == 0 {
		return true, nil
	}
	call, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	claimed := func() *gorm.DB {
		return db.Model(&mdb.PaymentConfirmation{}).Where("id = ? AND retry_at = ?", job.ID, lease)
	}
	var lookupFailed bool
	err = ConfirmPayment(call, feed, func(ctx context.Context, network, hash string, height int64) (json.RawMessage, error) {
		if job.VerifiedResult != "" {
			return json.RawMessage(job.VerifiedResult), nil
		}
		v, err := lookup(ctx, network, hash, height)
		if err != nil {
			lookupFailed = true
			return nil, err
		}
		// Persist a successful lookup so a settlement retry can reuse it.
		q := claimed().Update("verified_result", string(v))
		if q.Error != nil {
			return nil, q.Error
		}
		if q.RowsAffected != 1 {
			return nil, errors.New("confirmation lease changed")
		}
		return v, nil
	}, job)
	if errors.Is(err, ErrTransactionNotFound) || errors.Is(err, errPaymentMismatch) {
		return true, markPaymentForReview(db, job, lease, err)
	}
	if err != nil && !errors.Is(err, errInactiveCandidate) {
		if lookupFailed || errors.Is(err, errWaitingBlockDepth) {
			delay := time.Second
			updates := map[string]any{}
			if lookupFailed {
				// Back off to one minute, including for jobs created by older versions.
				delay = min(time.Minute, time.Second<<min(max(job.Attempts, 0), 6))
				updates["attempts"] = job.Attempts + 1
			}
			updates["retry_at"] = time.Now().Add(delay).UnixMilli()
			if e := claimed().Updates(updates).Error; e != nil {
				return false, e
			}
		}
		reason := "settlement_retry"
		if lookupFailed {
			reason = "lookup_retry"
		} else if errors.Is(err, errWaitingBlockDepth) {
			reason = "waiting_confirmations"
		}
		diagnostics.Record(diagnostics.Event{Event: "order.confirmation", Order: job.OrderID, Hash: job.Hash, Attempt: job.Attempts + 1, Outcome: reason})
		return true, fmt.Errorf("order=%d tx=%s: %w", job.OrderID, job.Hash, err)
	}
	err = db.Where("id = ? AND retry_at = ?", job.ID, lease).Delete(&mdb.PaymentConfirmation{}).Error
	return err == nil, err
}

func ConfirmPayment(ctx context.Context, feed scanner.Feed, lookup ObservedTransactionLookup, job mdb.PaymentConfirmation) error {
	var order mdb.Orders
	if err := dao.Mdb.WithContext(ctx).Where("id = ?", job.OrderID).Take(&order).Error; err != nil {
		return err
	}
	if order.ParentTradeId != "" {
		var parent mdb.Orders
		if err := dao.Mdb.WithContext(ctx).Where("trade_id = ?", order.ParentTradeId).Take(&parent).Error; err != nil {
			return err
		}
		if parent.Status != mdb.StatusConfirming {
			return errInactiveCandidate
		}
		if hasTransactionLock(&parent) {
			_ = data.ExtendOrderTransactionLock(&parent, time.Now().Add(config.GetOrderExpirationTimeDuration()))
		}
	}
	if order.Status != mdb.StatusConfirming {
		return errInactiveCandidate
	}
	if hasTransactionLock(&order) {
		_ = data.ExtendOrderTransactionLock(&order, time.Now().Add(config.GetOrderExpirationTimeDuration()))
	}
	var e scanner.Activity
	if err := json.Unmarshal([]byte(job.Activity), &e); err != nil {
		return err
	}
	if e.TxID != job.Hash || e.Network != order.Network {
		return errors.New("candidate identity mismatch")
	}
	chain, err := data.GetChainByNetwork(e.Network)
	if err != nil {
		return err
	}
	if chain.ID == 0 || !chain.Enabled {
		return errors.New("payment network is disabled")
	}
	ctx = diagnostics.WithOrder(ctx, order.TradeId, order.ParentTradeId, order.OrderId, order.ID)
	if e.Confirmations < int64(chain.MinConfirmations) {
		h, err := feed.Height(ctx, e.Network)
		if err != nil {
			return err
		}
		if h-e.Height+1 < int64(chain.MinConfirmations) {
			diag := diagnostics.Order(ctx)
			diag.Event = "order.depth"
			diag.Network = e.Network
			diag.Hash = e.TxID
			diag.Height = e.Height
			diag.NodeHead = &h
			diag.Confirmations = h - e.Height + 1
			diag.Required = int64(chain.MinConfirmations)
			diag.Outcome = "waiting"
			diagnostics.Record(diag)
			return errWaitingBlockDepth
		}
	}
	if !job.SkipStrict {
		if lookup == nil {
			return errors.New("transaction recheck unavailable")
		}
		if _, err := lookup(ctx, e.Network, e.TxID, e.Height); err != nil {
			return err
		}
	}
	// The hash lookup confirms existence. The matched scan event supplies the credit.
	blockID, err := verifiedOrderCredit(ctx, &order, e)
	if err != nil {
		return err
	}
	err = orderProcessingWithAllowedStatuses(&request.OrderProcessingRequest{TradeId: order.TradeId, BlockTransactionId: blockID}, []int{mdb.StatusConfirming})
	if errors.Is(err, constant.OrderStatusConflict) {
		return errInactiveCandidate
	}
	if err != nil {
		return err
	}
	sendPaymentNotification(&order)
	return nil
}

func verifiedOrderCredit(ctx context.Context, order *mdb.Orders, e scanner.Activity) (string, error) {
	dest, err := scanner.Canonical(order.Network, order.ReceiveAddress)
	if err != nil {
		return "", err
	}
	var blockID string
	err = visitCredits(ctx, e, func(_ scanner.Activity, addr string, t mdb.ChainToken, amount *big.Int, id string) error {
		a, err := scanner.Canonical(e.Network, addr)
		if err == nil && a == dest && strings.EqualFold(order.Token, t.Symbol) && amount != nil && amount.Sign() > 0 && amountMatchesRaw(order.ActualAmount, amount, t.Decimals) {
			blockID = id
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if blockID == "" {
		return "", errPaymentMismatch
	}
	return blockID, nil
}

func markPaymentForReview(db *gorm.DB, job mdb.PaymentConfirmation, lease int64, reason error) error {
	event := diagnostics.Event{Event: "order.review", Order: job.OrderID, Hash: job.Hash, Status: mdb.StatusManualReview, Outcome: "payment_mismatch"}
	if errors.Is(reason, ErrTransactionNotFound) {
		event.Outcome = "transaction_not_found"
	}
	changed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		q := tx.Where("id = ? AND retry_at = ?", job.ID, lease).Delete(&mdb.PaymentConfirmation{})
		if q.Error != nil || q.RowsAffected == 0 {
			return q.Error
		}
		var o mdb.Orders
		if err := tx.Select("trade_id", "parent_trade_id", "order_id", "network").Where("id = ?", job.OrderID).Take(&o).Error; err != nil {
			return err
		}
		event.Trade, event.Parent, event.MerchantOrder, event.Network = o.TradeId, o.ParentTradeId, o.OrderId, o.Network
		ids := []string{o.TradeId}
		if o.ParentTradeId != "" {
			ids = []string{o.ParentTradeId, o.TradeId}
		}
		for _, id := range ids {
			q := tx.Model(&mdb.Orders{}).Where("trade_id = ? AND status = ?", id, mdb.StatusConfirming).Updates(map[string]any{"status": mdb.StatusManualReview, "review_transaction_hash": job.Hash})
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected > 0 {
				changed = true
			}
		}
		return nil
	})
	if err == nil && changed {
		diagnostics.Record(event)
	}
	return err
}
