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
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StrictPair func(context.Context, string, string, string) (json.RawMessage, string, error)

type StrictLookup func(context.Context, string, string) (json.RawMessage, error)

// Installed during startup for subscription and manual on-chain payment checks.
var StrictTransaction StrictLookup

func queueConfirmation(order *mdb.Orders, e scanner.Activity) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return dao.Mdb.Transaction(func(tx *gorm.DB) error {
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
		// or redelivery of the same hash keeps the original strict decision.
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
}

// ErrStrictRoundUnsupported marks a scanner response that may have already used both rounds.
var ErrStrictRoundUnsupported = errors.New("scanner did not acknowledge single-round mode")

var errRoundLimit = errors.New("both strict rounds have been used")
var errPaymentMismatch = errors.New("strict result does not match order")
var errWaitingBlockDepth = errors.New("waiting for required block depth")

var errInactiveCandidate = errors.New("payment candidate no longer active")

// Each worker claims one due transfer. A crash releases the claim at its deadline.
// Failed lookups leave other orders and subsequent feed deliveries free to proceed.
func RunPaymentConfirmations(ctx context.Context, feed scanner.Feed, lookup StrictPair) {
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

func processDueConfirmation(ctx context.Context, feed scanner.Feed, lookup StrictPair) (bool, error) {
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
	// Reserve a round before I/O. Crashes consume the reserved round. A saved
	// quorum response can be replayed after a database or order-processing failure.
	claimed := func() *gorm.DB {
		return db.Model(&mdb.PaymentConfirmation{}).Where("id = ? AND retry_at = ?", job.ID, lease)
	}
	attempted := false
	err = ConfirmPayment(call, feed, func(ctx context.Context, network, hash string) (json.RawMessage, error) {
		if job.VerifiedResult != "" {
			return json.RawMessage(job.VerifiedResult), nil
		}
		if job.Attempts >= 2 {
			return nil, errRoundLimit
		}
		q := claimed().Where("attempts = ?", job.Attempts).Update("attempts", job.Attempts+1)
		if q.Error != nil {
			return nil, q.Error
		}
		if q.RowsAffected != 1 {
			return nil, errors.New("confirmation lease changed")
		}
		job.Attempts++
		v, pair, readErr := lookup(ctx, network, hash, job.PreviousPair)
		q = claimed().Updates(map[string]any{"previous_pair": pair, "verified_result": string(v)})
		if q.Error != nil {
			return nil, q.Error
		}
		if q.RowsAffected != 1 {
			return nil, errors.New("confirmation lease changed")
		}
		attempted = readErr != nil
		return v, readErr
	}, job)
	if job.SkipStrict && errors.Is(err, errPaymentMismatch) || errors.Is(err, ErrStrictRoundUnsupported) || errors.Is(err, errRoundLimit) || job.Attempts >= 2 && (attempted || errors.Is(err, errPaymentMismatch)) {
		return true, markPaymentForReview(db, job, lease)
	}
	if err != nil && !errors.Is(err, errInactiveCandidate) {
		if attempted || errors.Is(err, errPaymentMismatch) || errors.Is(err, errWaitingBlockDepth) {
			if e := claimed().Updates(map[string]any{"verified_result": "", "retry_at": time.Now().Add(time.Second).UnixMilli()}).Error; e != nil {
				return false, e
			}
		}
		return true, fmt.Errorf("order=%d tx=%s: %w", job.OrderID, job.Hash, err)
	}
	err = db.Where("id = ? AND retry_at = ?", job.ID, lease).Delete(&mdb.PaymentConfirmation{}).Error
	return err == nil, err
}

func ConfirmPayment(ctx context.Context, feed scanner.Feed, lookup StrictLookup, job mdb.PaymentConfirmation) error {
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
	if e.Confirmations < int64(chain.MinConfirmations) {
		h, err := feed.Height(ctx, e.Network)
		if err != nil {
			return err
		}
		if h-e.Height+1 < int64(chain.MinConfirmations) {
			return errWaitingBlockDepth
		}
	}
	verified := e
	if !job.SkipStrict {
		if lookup == nil {
			return errors.New("strict lookup unavailable")
		}
		result, err := lookup(ctx, e.Network, e.TxID)
		if err != nil {
			return err
		}
		verified, err = strictActivity(e, result)
		if err != nil {
			return fmt.Errorf("%w: %v", errPaymentMismatch, err)
		}
	}
	blockID, err := verifiedOrderCredit(ctx, &order, verified)
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

func markPaymentForReview(db *gorm.DB, job mdb.PaymentConfirmation, lease int64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		q := tx.Where("id = ? AND retry_at = ?", job.ID, lease).Delete(&mdb.PaymentConfirmation{})
		if q.Error != nil || q.RowsAffected == 0 {
			return q.Error
		}
		var o mdb.Orders
		if err := tx.Select("trade_id", "parent_trade_id").Where("id = ?", job.OrderID).Take(&o).Error; err != nil {
			return err
		}
		ids := []string{o.TradeId}
		if o.ParentTradeId != "" {
			ids = []string{o.ParentTradeId, o.TradeId}
		}
		for _, id := range ids {
			if err := tx.Model(&mdb.Orders{}).Where("trade_id = ? AND status = ?", id, mdb.StatusConfirming).Updates(map[string]any{"status": mdb.StatusManualReview, "review_transaction_hash": job.Hash}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
