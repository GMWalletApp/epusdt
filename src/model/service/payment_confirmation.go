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
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
		}
		digest := sha256.Sum256(payload) // A re-included transaction may have new block evidence.
		job := mdb.PaymentConfirmation{Position: hex.EncodeToString(digest[:]), OrderID: order.ID, Hash: e.TxID, Activity: string(payload), RetryAt: time.Now().UnixMilli()}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
	})
}

var errInactiveCandidate = errors.New("payment candidate no longer active")

// Each worker claims one due transfer. A crash releases the claim at its deadline.
// Failed lookups leave other orders and subsequent feed deliveries free to proceed.
func RunPaymentConfirmations(ctx context.Context, feed scanner.Feed, lookup StrictLookup) {
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

func processDueConfirmation(ctx context.Context, feed scanner.Feed, lookup StrictLookup) (bool, error) {
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
	err = ConfirmPayment(call, feed, lookup, job)
	if err != nil && !errors.Is(err, errInactiveCandidate) {
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
	if order.Status != mdb.StatusConfirming {
		return errInactiveCandidate
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
			return errors.New("waiting for required block depth")
		}
	}
	if lookup == nil {
		return errors.New("strict lookup unavailable")
	}
	result, err := lookup(ctx, e.Network, e.TxID)
	if err != nil {
		return err
	}
	verified, err := strictActivity(e, result)
	if err != nil {
		return err
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
		return "", errors.New("strict transfer does not match payment address, asset and amount")
	}
	return blockID, nil
}
