package data

import (
	"fmt"
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// DailyStat is one bucket of day-level aggregation used by the
// dashboard trend charts.
// Field "Day" holds "YYYY-MM-DD" for daily buckets and
// "YYYY-MM-DD HH:00" for hourly buckets.
type DailyStat struct {
	Day          string  `json:"day"` // "YYYY-MM-DD" (daily) or "YYYY-MM-DD HH:00" (hourly)
	OrderCount   int64   `json:"order_count"`
	SuccessCount int64   `json:"success_count"`
	TotalAmount  float64 `json:"total_amount"`  // sum of amount (fiat)
	ActualAmount float64 `json:"actual_amount"` // sum of actual_amount (token)
}

// AddressDailyStat buckets actual_amount by (day, receive_address) for
// the stacked asset-trend chart.
// Field "Day" holds "YYYY-MM-DD" (daily) or "YYYY-MM-DD HH:00" (hourly).
type AddressDailyStat struct {
	Day          string  `json:"day"`
	Address      string  `json:"address"`
	ActualAmount float64 `json:"actual_amount"`
}

// Order timestamps may use either the historical Go offset suffix or the
// current UTC representation. Normalize the instant before applying the
// server-local label used by parseRange.
func statsBucketExpr(hourly bool) string {
	timestamp := SQLiteTimestampUTCExpr("created_at")
	if hourly {
		return "strftime('%Y-%m-%d %H:00', " + timestamp + ", 'localtime')"
	}
	return "strftime('%Y-%m-%d', " + timestamp + ", 'localtime')"
}

// DailyOrderStats returns per-day counts/amounts within [start, end],
// zero-filling any days in the range that have no orders.
// Only orders in StatusPaySuccess contribute to Amount sums — that's
// what the user means by "流水". Pending orders count toward OrderCount
// to keep成交率 computable.
func DailyOrderStats(start, end time.Time) ([]DailyStat, error) {
	rows, err := queryOrderStats(start, end, false)
	if err != nil {
		return nil, err
	}
	return fillDailyStats(start, end, rows), nil
}

// HourlyOrderStats returns per-hour counts/amounts within [start, end],
// zero-filling any hours that have no orders.
func HourlyOrderStats(start, end time.Time) ([]DailyStat, error) {
	rows, err := queryOrderStats(start, end, true)
	if err != nil {
		return nil, err
	}
	return fillHourlyStats(start, end, rows), nil
}

func queryOrderStats(start, end time.Time, hourly bool) ([]DailyStat, error) {
	// Normalize the bounds and stored text to UTC before comparing them.
	start, end = start.UTC(), end.UTC()
	var rows []DailyStat
	bucket := statsBucketExpr(hourly)
	err := merchantOrders().
		Select(fmt.Sprintf(`%s AS day,
            COUNT(*) AS order_count,
            SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_count,
            SUM(CASE WHEN status = ? THEN amount ELSE 0 END) AS total_amount,
            SUM(CASE WHEN status = ? THEN actual_amount ELSE 0 END) AS actual_amount`, bucket),
			mdb.StatusPaySuccess, mdb.StatusPaySuccess, mdb.StatusPaySuccess).
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Group(bucket).
		Order("day ASC").
		Scan(&rows).Error
	return rows, err
}

// fillDailyStats generates a zero-filled slice covering every calendar
// day in [start, end]. Days present in rows overwrite the zero entries.
func fillDailyStats(start, end time.Time, rows []DailyStat) []DailyStat {
	byDay := make(map[string]DailyStat, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}
	var out []DailyStat
	cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	last := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	for !cur.After(last) {
		key := cur.Format("2006-01-02")
		if r, ok := byDay[key]; ok {
			out = append(out, r)
		} else {
			out = append(out, DailyStat{Day: key})
		}
		cur = cur.AddDate(0, 0, 1)
	}
	return out
}

// fillHourlyStats generates a zero-filled slice covering every whole
// hour in [start, end]. Hours present in rows overwrite the zero entries.
func fillHourlyStats(start, end time.Time, rows []DailyStat) []DailyStat {
	byHour := make(map[string]DailyStat, len(rows))
	for _, r := range rows {
		byHour[r.Day] = r
	}
	var out []DailyStat
	cur := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), 0, 0, 0, start.Location())
	endHour := time.Date(end.Year(), end.Month(), end.Day(), end.Hour(), 0, 0, 0, end.Location())
	for !cur.After(endHour) {
		key := cur.Format("2006-01-02 15:00")
		if r, ok := byHour[key]; ok {
			out = append(out, r)
		} else {
			out = append(out, DailyStat{Day: key})
		}
		cur = cur.Add(time.Hour)
	}
	return out
}

// DailyAssetByAddress groups paid actual_amount by (day, receive_address)
// for the per-address stacked chart, zero-filling missing days.
func DailyAssetByAddress(start, end time.Time) ([]AddressDailyStat, error) {
	rows, err := queryAssetByAddress(start, end, false)
	if err != nil {
		return nil, err
	}
	return fillAddressDailyStats(start, end, rows), nil
}

// HourlyAssetByAddress groups paid actual_amount by (hour, receive_address)
// for the per-address stacked chart, zero-filling missing hours.
func HourlyAssetByAddress(start, end time.Time) ([]AddressDailyStat, error) {
	rows, err := queryAssetByAddress(start, end, true)
	if err != nil {
		return nil, err
	}
	return fillAddressHourlyStats(start, end, rows), nil
}

func queryAssetByAddress(start, end time.Time, hourly bool) ([]AddressDailyStat, error) {
	start, end = start.UTC(), end.UTC()
	var rows []AddressDailyStat
	bucket := statsBucketExpr(hourly)
	err := merchantOrders().
		Select(fmt.Sprintf(`%s AS day,
            receive_address AS address,
            SUM(actual_amount) AS actual_amount`, bucket)).
		Where("status = ?", mdb.StatusPaySuccess).
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Group(bucket).
		Group("receive_address").
		Order("day ASC, address ASC").
		Scan(&rows).Error
	return rows, err
}

// fillAddressDailyStats zero-fills missing day×address combinations.
// All addresses that appear in rows are represented for every day in range.
func fillAddressDailyStats(start, end time.Time, rows []AddressDailyStat) []AddressDailyStat {
	return fillAddressStats(start, end, rows, false)
}

// fillAddressHourlyStats zero-fills missing hour×address combinations.
func fillAddressHourlyStats(start, end time.Time, rows []AddressDailyStat) []AddressDailyStat {
	return fillAddressStats(start, end, rows, true)
}

func fillAddressStats(start, end time.Time, rows []AddressDailyStat, hourly bool) []AddressDailyStat {
	type key struct{ day, addr string }
	byKey := make(map[key]float64, len(rows))
	addrSet := make(map[string]struct{}, 4)
	for _, r := range rows {
		byKey[key{r.Day, r.Address}] = r.ActualAmount
		addrSet[r.Address] = struct{}{}
	}
	if len(addrSet) == 0 {
		return rows
	}
	var periods []string
	if hourly {
		cur := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), 0, 0, 0, start.Location())
		endH := time.Date(end.Year(), end.Month(), end.Day(), end.Hour(), 0, 0, 0, end.Location())
		for !cur.After(endH) {
			periods = append(periods, cur.Format("2006-01-02 15:00"))
			cur = cur.Add(time.Hour)
		}
	} else {
		cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
		last := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
		for !cur.After(last) {
			periods = append(periods, cur.Format("2006-01-02"))
			cur = cur.AddDate(0, 0, 1)
		}
	}
	var out []AddressDailyStat
	for _, day := range periods {
		for addr := range addrSet {
			out = append(out, AddressDailyStat{
				Day:          day,
				Address:      addr,
				ActualAmount: byKey[key{day, addr}],
			})
		}
	}
	return out
}

// SumPaidActualAmount returns the total paid actual_amount across all
// time. Used by the overview "total asset" card.
func SumPaidActualAmount() (float64, error) {
	var sum float64
	err := merchantOrders().
		Select("COALESCE(SUM(actual_amount), 0)").
		Where("status = ?", mdb.StatusPaySuccess).
		Scan(&sum).Error
	return sum, err
}

// PaidStatsInRange returns (order_count, success_count, actual_sum) for
// a given time range. Caller chooses the range (today / last 7 days).
func PaidStatsInRange(start, end time.Time) (int64, int64, float64, error) {
	start, end = start.UTC(), end.UTC()
	type row struct {
		OrderCount   int64
		SuccessCount int64
		ActualSum    float64
	}
	var r row
	err := merchantOrders().
		Select(`COUNT(*) AS order_count,
            SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_count,
            SUM(CASE WHEN status = ? THEN actual_amount ELSE 0 END) AS actual_sum`,
			mdb.StatusPaySuccess, mdb.StatusPaySuccess).
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Scan(&r).Error
	return r.OrderCount, r.SuccessCount, r.ActualSum, err
}

// ActiveAddressCountInRange returns the number of distinct wallet addresses
// that appear in at least one order within [start, end].
func ActiveAddressCountInRange(start, end time.Time) (int64, error) {
	start, end = start.UTC(), end.UTC()
	var count int64
	err := merchantOrders().
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Distinct("receive_address").
		Count(&count).Error
	return count, err
}

// AveragePaymentDurationSeconds returns the mean elapsed time between
// created_at and updated_at for orders that reached StatusPaySuccess
// in the given range. Zero-safe when no rows match.
func AveragePaymentDurationSeconds(start, end time.Time) (float64, error) {
	start, end = start.UTC(), end.UTC()
	type row struct {
		Avg float64
	}
	var r row
	type pair struct {
		CreatedAt time.Time
		UpdatedAt time.Time
	}
	var pairs []pair
	err := merchantOrders().
		Select("created_at, updated_at").
		Where("status = ?", mdb.StatusPaySuccess).
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Scan(&pairs).Error
	if err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}
	total := 0.0
	for _, p := range pairs {
		total += p.UpdatedAt.Sub(p.CreatedAt).Seconds()
	}
	r.Avg = total / float64(len(pairs))
	return r.Avg, nil
}

// CountExpiredInRange returns the number of StatusExpired orders in range.
func CountExpiredInRange(start, end time.Time) (int64, error) {
	start, end = start.UTC(), end.UTC()
	var n int64
	err := merchantOrders().
		Where("status = ?", mdb.StatusExpired).
		Where(orderCreatedAtUTCExpr+" >= ?", orderTimeUTC(start)).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(end)).
		Count(&n).Error
	return n, err
}

// RecentOrders returns the latest N orders regardless of status.
func RecentOrders(limit int) ([]mdb.Orders, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var rows []mdb.Orders
	err := merchantOrders().
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// CountEnabledChains counts chains where Enabled=true.
func CountEnabledChains() (int64, error) {
	var n int64
	err := dao.Mdb.Model(&mdb.Chain{}).Where("enabled = ?", true).Count(&n).Error
	return n, err
}
