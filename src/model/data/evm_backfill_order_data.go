package data

import (
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// ListEvmBackfillOrders includes expired orders relative to the last covered
// block, NOT wall time. An outage must never age an unscanned payment out of
// recovery. Paid rows retain the same trailing scan window for deduplication.
// Use Orders' timestamp scanner and soft-delete scope; do not compare SQLite
// datetime strings from different timezones in this safety-critical filter.
func ListEvmBackfillOrders(network string, coveredThrough time.Time, expiration, grace time.Duration) ([]mdb.Orders, error) {
	var rows []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Select("id, created_at, status").
		Where("network = ?", network).
		Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusPaySuccess, mdb.StatusExpired}).
		Where("(pay_provider = ? OR pay_provider = '')", mdb.PaymentProviderOnChain).
		Where("receive_address <> '' AND token <> '' AND actual_amount > 0").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]mdb.Orders, 0, len(rows))
	for _, row := range rows {
		// Invalid timestamps must conservatively keep the scanner active.
		created := time.UnixMilli(row.CreatedAt.TimestampMilli())
		if row.Status == mdb.StatusWaitPay || row.CreatedAt.IsZero() || !created.Add(expiration+grace).Before(coveredThrough) {
			result = append(result, row)
		}
	}
	return result, nil
}
