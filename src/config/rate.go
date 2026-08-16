package config

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/util/http_client"
	"golang.org/x/sync/singleflight"
)

const (
	RateModeFixed          = "fixed"
	RateModeAuto           = "auto"
	DefaultRateCacheTTL    = 300
	MinRateCacheTTLSeconds = 10
	MaxRateCacheTTLSeconds = 86400
)

type RateRefreshResult struct {
	Base          string             `json:"base" example:"cny"`
	OK            bool               `json:"ok"`
	Skipped       bool               `json:"skipped,omitempty"`
	Rates         map[string]float64 `json:"rates,omitempty"`
	LastSuccessAt *time.Time         `json:"last_success_at,omitempty" example:"2026-07-22T10:00:00Z"`
	LastAttemptAt *time.Time         `json:"last_attempt_at,omitempty" example:"2026-07-22T10:05:00Z"`
	Error         string             `json:"error,omitempty" example:"call rate api unexpected status: 502 Bad Gateway"`
}

type RateBaseStatus struct {
	Base          string             `json:"base" example:"cny"`
	Rates         map[string]float64 `json:"rates"`
	SourceURL     string             `json:"source_url" example:"https://cdn.example.com/currencies/"`
	IsStale       bool               `json:"is_stale"`
	LastSuccessAt *time.Time         `json:"last_success_at,omitempty" example:"2026-07-22T10:00:00Z"`
	LastAttemptAt *time.Time         `json:"last_attempt_at,omitempty" example:"2026-07-22T10:05:00Z"`
	LastRefreshOK bool               `json:"last_refresh_ok"`
	LastError     string             `json:"last_error" example:"call rate api unexpected status: 502 Bad Gateway"`
}

type RateStatus struct {
	Mode            string           `json:"mode" enums:"fixed,auto" example:"auto"`
	APIURL          string           `json:"api_url" example:"https://cdn.example.com/currencies/"`
	CacheTTLSeconds int              `json:"cache_ttl_seconds" minimum:"10" maximum:"86400" example:"300"`
	Bases           []RateBaseStatus `json:"bases"`
}

var (
	rateCacheMu     sync.RWMutex
	rateCacheMemory = make(map[string]RateCacheSnapshot)
	rateRefreshes   singleflight.Group
	rateNow         = time.Now
)

func GetRateMode() string {
	if strings.EqualFold(strings.TrimSpace(settingsRateMode()), RateModeAuto) {
		return RateModeAuto
	}
	return RateModeFixed
}

func GetRateCacheTTLSeconds() int {
	ttl, err := strconv.Atoi(strings.TrimSpace(settingsRateCacheTTLSeconds()))
	if err != nil || ttl < MinRateCacheTTLSeconds || ttl > MaxRateCacheTTLSeconds {
		return DefaultRateCacheTTL
	}
	return ttl
}

func GetRateForCoin(coin string, base string) float64 {
	coin = normalizeRateKey(coin)
	base = normalizeRateKey(base)
	if coin == "" || base == "" {
		return 0
	}
	if coin == base {
		return 1
	}
	if GetRateMode() == RateModeFixed {
		if forcedRate := getForcedRateForCoin(coin, base); forcedRate > 0 {
			return forcedRate
		}
		if coin == "usdt" && base == "usd" {
			return 1
		}
		return 0
	}
	if coin == "usdt" && base == "usd" {
		return 1
	}

	snapshot, found := loadRateCacheSnapshot(base)
	rate := rateFromSnapshot(snapshot, coin)
	now := rateNow()
	if found && rate > 0 {
		if isRateCacheStale(snapshot, now) && isRateRefreshDue(snapshot, now) {
			go func() {
				_ = refreshRateBaseForCoin(base, coin, false)
			}()
		}
		return rate
	}

	// A successful, fresh full-base payload that does not contain the target
	// coin is authoritative until its TTL expires.
	if found && !isRateCacheStale(snapshot, now) {
		return 0
	}
	_ = refreshRateBaseForCoin(base, coin, false)
	snapshot, _ = loadRateCacheSnapshot(base)
	return rateFromSnapshot(snapshot, coin)
}

func GetUsdtRate() float64 {
	rate := GetRateForCoin("usdt", "cny")
	if rate <= 0 {
		log.Printf("usdt/cny rate unavailable in %s mode", GetRateMode())
		return 0
	}
	return 1 / rate
}

func RefreshRateBase(base string, force bool) RateRefreshResult {
	return refreshRateBaseForCoin(base, "", force)
}

func refreshRateBaseForCoin(base string, requiredCoin string, force bool) RateRefreshResult {
	base = normalizeRateKey(base)
	requiredCoin = normalizeRateKey(requiredCoin)
	if base == "" {
		return RateRefreshResult{Base: base, Error: "base currency is empty"}
	}
	if !force {
		snapshot, _ := loadRateCacheSnapshot(base)
		now := rateNow()
		if !isRateCacheStale(snapshot, now) || !isRateRefreshDue(snapshot, now) {
			return refreshResultFromSnapshot(snapshot, true)
		}
	}
	value, _, _ := rateRefreshes.Do(base, func() (interface{}, error) {
		return refreshRateBase(base, requiredCoin), nil
	})
	return value.(RateRefreshResult)
}

func refreshRateBase(base string, requiredCoin string) RateRefreshResult {
	snapshot, _ := loadRateCacheSnapshot(base)
	previous := cloneSnapshot(snapshot)
	rates, sourceURL, err := fetchRatesForBase(base)
	attemptedAt := rateNow()
	snapshot.Base = base
	snapshot.LastAttemptAt = timePointer(attemptedAt)
	if err == nil && requiredCoin != "" && rateFromRates(rates, requiredCoin) <= 0 {
		err = fmt.Errorf("rate api response has no positive %s.%s rate", base, requiredCoin)
	}
	if err != nil {
		snapshot.LastRefreshOK = false
		snapshot.LastError = err.Error()
		if saveErr := storeRateCacheSnapshot(snapshot); saveErr != nil {
			snapshot.LastError += "; persist cache status: " + saveErr.Error()
			cacheRateSnapshotInMemory(snapshot)
		}
		log.Printf("refresh rate cache for %s failed: %s", base, snapshot.LastError)
		return refreshResultFromSnapshot(snapshot, false)
	}

	// A provider may temporarily return a partial payload. Keep previously
	// successful coins that are absent from the new response so one incomplete
	// refresh cannot make an otherwise usable payment asset unavailable.
	snapshot.Rates = mergeRateMaps(rates, previous.Rates)
	snapshot.SourceURL = sourceURL
	snapshot.LastSuccessAt = timePointer(attemptedAt)
	snapshot.LastRefreshOK = true
	snapshot.LastError = ""
	if err := storeRateCacheSnapshot(snapshot); err != nil {
		// Do not publish an API result that was not persisted. Continue serving
		// the last durable snapshot and expose the failed attempt in memory.
		previous.Base = base
		previous.LastAttemptAt = timePointer(attemptedAt)
		previous.LastRefreshOK = false
		previous.LastError = "persist rate cache: " + err.Error()
		cacheRateSnapshotInMemory(previous)
		return refreshResultFromSnapshot(previous, false)
	}
	return refreshResultFromSnapshot(snapshot, false)
}

func fetchRatesForBase(base string) (map[string]float64, string, error) {
	baseURL := strings.TrimSpace(GetRateApiUrl())
	if baseURL == "" {
		return nil, "", fmt.Errorf("rate api url is empty")
	}
	requestURL := strings.TrimRight(baseURL, "/") + "/" + url.PathEscape(base) + ".json"
	resp, err := http_client.GetHttpClient().R().Get(requestURL)
	if err != nil {
		return nil, baseURL, fmt.Errorf("call rate api: %w", err)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, baseURL, fmt.Errorf("call rate api unexpected status: %s", resp.Status())
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		return nil, baseURL, fmt.Errorf("decode rate api response: %w", err)
	}
	var rawBase json.RawMessage
	for key, candidate := range payload {
		if normalizeRateKey(key) != base {
			continue
		}
		if rawBase != nil {
			return nil, baseURL, fmt.Errorf("rate api response contains duplicate normalized base %s", base)
		}
		rawBase = candidate
	}
	if rawBase == nil {
		return nil, baseURL, fmt.Errorf("rate api response has no %s object", base)
	}
	var rawRates map[string]float64
	if err := json.Unmarshal(rawBase, &rawRates); err != nil {
		return nil, baseURL, fmt.Errorf("decode %s rate object: %w", base, err)
	}
	rates := make(map[string]float64, len(rawRates))
	for coin, rate := range rawRates {
		coin = normalizeRateKey(coin)
		if coin == "" || rate <= 0 {
			continue
		}
		if _, exists := rates[coin]; exists {
			return nil, baseURL, fmt.Errorf("rate api response contains duplicate normalized coin %s.%s", base, coin)
		}
		rates[coin] = rate
	}
	if len(rates) == 0 {
		return nil, baseURL, fmt.Errorf("rate api response has no positive %s rates", base)
	}
	return rates, baseURL, nil
}

func GetRateStatus() RateStatus {
	snapshots := listRateCacheSnapshots()
	now := rateNow()
	bases := make([]RateBaseStatus, 0, len(snapshots))
	for _, snapshot := range snapshots {
		bases = append(bases, RateBaseStatus{
			Base:          snapshot.Base,
			Rates:         cloneRates(snapshot.Rates),
			SourceURL:     snapshot.SourceURL,
			IsStale:       isRateCacheStale(snapshot, now),
			LastSuccessAt: snapshot.LastSuccessAt,
			LastAttemptAt: snapshot.LastAttemptAt,
			LastRefreshOK: snapshot.LastRefreshOK,
			LastError:     snapshot.LastError,
		})
	}
	return RateStatus{
		Mode:            GetRateMode(),
		APIURL:          GetRateApiUrl(),
		CacheTTLSeconds: GetRateCacheTTLSeconds(),
		Bases:           bases,
	}
}

func KnownRateBases() []string {
	set := make(map[string]struct{})
	for _, snapshot := range listRateCacheSnapshots() {
		if snapshot.Base != "" {
			set[snapshot.Base] = struct{}{}
		}
	}
	var forced map[string]json.RawMessage
	if json.Unmarshal([]byte(settingsForcedRateList()), &forced) == nil {
		for base := range forced {
			if normalized := normalizeRateKey(base); normalized != "" {
				set[normalized] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		set["cny"] = struct{}{}
	}
	bases := make([]string, 0, len(set))
	for base := range set {
		bases = append(bases, base)
	}
	sort.Strings(bases)
	return bases
}

func ResetRateCacheRuntime() {
	rateCacheMu.Lock()
	rateCacheMemory = make(map[string]RateCacheSnapshot)
	rateCacheMu.Unlock()
}

func getForcedRateForCoin(coin string, base string) float64 {
	var rates map[string]map[string]float64
	if json.Unmarshal([]byte(settingsForcedRateList()), &rates) != nil {
		return 0
	}
	for configuredBase, coins := range rates {
		if normalizeRateKey(configuredBase) != base {
			continue
		}
		for configuredCoin, rate := range coins {
			if normalizeRateKey(configuredCoin) == coin && rate > 0 {
				return rate
			}
		}
	}
	return 0
}

func loadRateCacheSnapshot(base string) (RateCacheSnapshot, bool) {
	rateCacheMu.RLock()
	snapshot, ok := rateCacheMemory[base]
	rateCacheMu.RUnlock()
	if ok {
		return cloneSnapshot(snapshot), true
	}
	if RateCacheLoad == nil {
		return RateCacheSnapshot{Base: base}, false
	}
	loaded, err := RateCacheLoad(base)
	if err != nil {
		return RateCacheSnapshot{Base: base}, false
	}
	loaded.Base = base
	rateCacheMu.Lock()
	if current, exists := rateCacheMemory[base]; exists {
		rateCacheMu.Unlock()
		return cloneSnapshot(current), true
	}
	rateCacheMemory[base] = cloneSnapshot(loaded)
	rateCacheMu.Unlock()
	return cloneSnapshot(loaded), true
}

func listRateCacheSnapshots() []RateCacheSnapshot {
	if RateCacheLoadAll != nil {
		if loaded, err := RateCacheLoadAll(); err == nil {
			rateCacheMu.Lock()
			for _, snapshot := range loaded {
				snapshot.Base = normalizeRateKey(snapshot.Base)
				if snapshot.Base == "" {
					continue
				}
				current, exists := rateCacheMemory[snapshot.Base]
				if !exists || isSnapshotNewer(snapshot, current) {
					rateCacheMemory[snapshot.Base] = cloneSnapshot(snapshot)
				}
			}
			rateCacheMu.Unlock()
		}
	}
	rateCacheMu.RLock()
	out := make([]RateCacheSnapshot, 0, len(rateCacheMemory))
	for _, snapshot := range rateCacheMemory {
		out = append(out, cloneSnapshot(snapshot))
	}
	rateCacheMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Base < out[j].Base })
	return out
}

func storeRateCacheSnapshot(snapshot RateCacheSnapshot) error {
	snapshot.Base = normalizeRateKey(snapshot.Base)
	if RateCacheSave != nil {
		if err := RateCacheSave(snapshot); err != nil {
			return err
		}
	}
	cacheRateSnapshotInMemory(snapshot)
	return nil
}

func cacheRateSnapshotInMemory(snapshot RateCacheSnapshot) {
	rateCacheMu.Lock()
	rateCacheMemory[snapshot.Base] = cloneSnapshot(snapshot)
	rateCacheMu.Unlock()
}

func rateFromSnapshot(snapshot RateCacheSnapshot, coin string) float64 {
	return rateFromRates(snapshot.Rates, coin)
}

func rateFromRates(rates map[string]float64, coin string) float64 {
	if rate := rates[coin]; rate > 0 {
		return rate
	}
	if coin == "ton" {
		return rates["toncoin"]
	}
	return 0
}

func mergeRateMaps(primary, fallback map[string]float64) map[string]float64 {
	merged := cloneRates(primary)
	for coin, rate := range fallback {
		if _, exists := merged[coin]; !exists && rate > 0 {
			merged[coin] = rate
		}
	}
	return merged
}

func isSnapshotNewer(candidate, current RateCacheSnapshot) bool {
	candidateTime := latestSnapshotTime(candidate)
	currentTime := latestSnapshotTime(current)
	return candidateTime.After(currentTime)
}

func latestSnapshotTime(snapshot RateCacheSnapshot) time.Time {
	latest := time.Time{}
	if snapshot.LastSuccessAt != nil && snapshot.LastSuccessAt.After(latest) {
		latest = *snapshot.LastSuccessAt
	}
	if snapshot.LastAttemptAt != nil && snapshot.LastAttemptAt.After(latest) {
		latest = *snapshot.LastAttemptAt
	}
	return latest
}

func isRateCacheStale(snapshot RateCacheSnapshot, now time.Time) bool {
	if snapshot.LastSuccessAt == nil || len(snapshot.Rates) == 0 {
		return true
	}
	return !now.Before(snapshot.LastSuccessAt.Add(time.Duration(GetRateCacheTTLSeconds()) * time.Second))
}

func isRateRefreshDue(snapshot RateCacheSnapshot, now time.Time) bool {
	if snapshot.LastAttemptAt == nil {
		return true
	}
	return !now.Before(snapshot.LastAttemptAt.Add(time.Duration(GetRateCacheTTLSeconds()) * time.Second))
}

func refreshResultFromSnapshot(snapshot RateCacheSnapshot, skipped bool) RateRefreshResult {
	return RateRefreshResult{
		Base:          snapshot.Base,
		OK:            snapshot.LastRefreshOK,
		Skipped:       skipped,
		Rates:         cloneRates(snapshot.Rates),
		LastSuccessAt: snapshot.LastSuccessAt,
		LastAttemptAt: snapshot.LastAttemptAt,
		Error:         snapshot.LastError,
	}
}

func normalizeRateKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneSnapshot(snapshot RateCacheSnapshot) RateCacheSnapshot {
	snapshot.Rates = cloneRates(snapshot.Rates)
	return snapshot
}

func cloneRates(rates map[string]float64) map[string]float64 {
	if rates == nil {
		return map[string]float64{}
	}
	cloned := make(map[string]float64, len(rates))
	for coin, rate := range rates {
		cloned[coin] = rate
	}
	return cloned
}

func timePointer(value time.Time) *time.Time {
	return &value
}
