package data

import (
	"encoding/json"
	"strings"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm/clause"
)

func GetRateCache(base string) (mdb.RateCache, error) {
	var row mdb.RateCache
	err := dao.Mdb.Where("base = ?", strings.ToLower(strings.TrimSpace(base))).Take(&row).Error
	return row, err
}

func ListRateCaches() ([]mdb.RateCache, error) {
	var rows []mdb.RateCache
	err := dao.Mdb.Order("base ASC").Find(&rows).Error
	return rows, err
}

func SaveRateCache(row mdb.RateCache) error {
	row.Base = strings.ToLower(strings.TrimSpace(row.Base))
	return dao.Mdb.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "base"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"rates",
			"source_url",
			"last_success_at",
			"last_attempt_at",
			"last_refresh_ok",
			"last_error",
			"updated_at",
		}),
	}).Create(&row).Error
}

func LoadRateCacheSnapshot(base string) (config.RateCacheSnapshot, error) {
	row, err := GetRateCache(base)
	if err != nil {
		return config.RateCacheSnapshot{}, err
	}
	return rateCacheSnapshot(row), nil
}

func ListRateCacheSnapshots() ([]config.RateCacheSnapshot, error) {
	rows, err := ListRateCaches()
	if err != nil {
		return nil, err
	}
	out := make([]config.RateCacheSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, rateCacheSnapshot(row))
	}
	return out, nil
}

func SaveRateCacheSnapshot(snapshot config.RateCacheSnapshot) error {
	rates, err := json.Marshal(snapshot.Rates)
	if err != nil {
		return err
	}
	return SaveRateCache(mdb.RateCache{
		Base:          snapshot.Base,
		Rates:         string(rates),
		SourceURL:     snapshot.SourceURL,
		LastSuccessAt: snapshot.LastSuccessAt,
		LastAttemptAt: snapshot.LastAttemptAt,
		LastRefreshOK: snapshot.LastRefreshOK,
		LastError:     snapshot.LastError,
	})
}

func rateCacheSnapshot(row mdb.RateCache) config.RateCacheSnapshot {
	rates := make(map[string]float64)
	_ = json.Unmarshal([]byte(row.Rates), &rates)
	return config.RateCacheSnapshot{
		Base:          row.Base,
		Rates:         rates,
		SourceURL:     row.SourceURL,
		LastSuccessAt: row.LastSuccessAt,
		LastAttemptAt: row.LastAttemptAt,
		LastRefreshOK: row.LastRefreshOK,
		LastError:     row.LastError,
	}
}
