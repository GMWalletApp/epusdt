package config

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testRateCacheStore struct {
	mu   sync.Mutex
	rows map[string]RateCacheSnapshot
}

func installRateCacheStore(t *testing.T, initial ...RateCacheSnapshot) *testRateCacheStore {
	t.Helper()
	store := &testRateCacheStore{rows: make(map[string]RateCacheSnapshot)}
	for _, row := range initial {
		store.rows[row.Base] = cloneSnapshot(row)
	}
	oldLoad, oldLoadAll, oldSave := RateCacheLoad, RateCacheLoadAll, RateCacheSave
	RateCacheLoad = func(base string) (RateCacheSnapshot, error) {
		store.mu.Lock()
		defer store.mu.Unlock()
		row, ok := store.rows[base]
		if !ok {
			return RateCacheSnapshot{}, errTestCacheMiss
		}
		return cloneSnapshot(row), nil
	}
	RateCacheLoadAll = func() ([]RateCacheSnapshot, error) {
		store.mu.Lock()
		defer store.mu.Unlock()
		rows := make([]RateCacheSnapshot, 0, len(store.rows))
		for _, row := range store.rows {
			rows = append(rows, cloneSnapshot(row))
		}
		return rows, nil
	}
	RateCacheSave = func(snapshot RateCacheSnapshot) error {
		store.mu.Lock()
		store.rows[snapshot.Base] = cloneSnapshot(snapshot)
		store.mu.Unlock()
		return nil
	}
	ResetRateCacheRuntime()
	t.Cleanup(func() {
		RateCacheLoad, RateCacheLoadAll, RateCacheSave = oldLoad, oldLoadAll, oldSave
		ResetRateCacheRuntime()
	})
	return store
}

var errTestCacheMiss = &testRateError{"cache miss"}

type testRateError struct{ message string }

func (e *testRateError) Error() string { return e.message }

func TestAutoRatePersistsFullBasePayloadAcrossRuntimeReset(t *testing.T) {
	installSettingsGetter(t, map[string]string{
		"rate.mode":              RateModeAuto,
		"rate.api_url":           "https://rate.example.test",
		"rate.cache_ttl_seconds": "300",
	})
	store := installRateCacheStore(t)
	var calls atomic.Int32
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.14635,"usdc":0.1463,"trx":1.82}}`)),
			Request:    r,
		}, nil
	})

	if got := GetRateForCoin("usdt", "cny"); got != 0.14635 {
		t.Fatalf("first rate = %v, want 0.14635", got)
	}
	ResetRateCacheRuntime()
	if got := GetRateForCoin("trx", "cny"); got != 1.82 {
		t.Fatalf("persisted rate = %v, want 1.82", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("API calls = %d, want 1", calls.Load())
	}
	store.mu.Lock()
	stored := store.rows["cny"]
	store.mu.Unlock()
	if len(stored.Rates) != 3 || stored.LastSuccessAt == nil || !stored.LastRefreshOK {
		t.Fatalf("stored snapshot = %#v", stored)
	}
}

func TestAutoRateReturnsStaleCacheWhileAsyncRefreshFails(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldNow := rateNow
	rateNow = func() time.Time { return now }
	t.Cleanup(func() { rateNow = oldNow })
	installSettingsGetter(t, map[string]string{
		"rate.mode":              RateModeAuto,
		"rate.api_url":           "https://rate.example.test",
		"rate.cache_ttl_seconds": "10",
	})
	lastSuccess := now.Add(-time.Minute)
	lastAttempt := lastSuccess
	store := installRateCacheStore(t, RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14},
		LastSuccessAt: &lastSuccess,
		LastAttemptAt: &lastAttempt,
		LastRefreshOK: true,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("stale fallback = %v, want 0.14", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("asynchronous refresh did not start")
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		row := store.rows["cny"]
		store.mu.Unlock()
		if row.LastAttemptAt != nil && !row.LastRefreshOK && row.LastError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed refresh status was not persisted: %#v", row)
		}
		time.Sleep(time.Millisecond)
	}
	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("fallback after failure = %v, want 0.14", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("API calls within retry TTL = %d, want 1", calls.Load())
	}
}

func TestForcedRefreshRejectsAmbiguousPayloadAndPreservesCache(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldSuccess := now.Add(-time.Hour)
	installSettingsGetter(t, map[string]string{
		"rate.mode":    RateModeAuto,
		"rate.api_url": "https://rate.example.test",
	})
	store := installRateCacheStore(t, RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14},
		LastSuccessAt: &oldSuccess,
		LastRefreshOK: true,
	})
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"USDT":0.15,"usdt":0.16}}`)),
			Request:    r,
		}, nil
	})

	result := RefreshRateBase("cny", true)
	if result.OK || !strings.Contains(result.Error, "duplicate normalized coin") {
		t.Fatalf("ambiguous refresh result = %#v", result)
	}
	store.mu.Lock()
	stored := store.rows["cny"]
	store.mu.Unlock()
	if stored.Rates["usdt"] != 0.14 || stored.LastSuccessAt == nil || !stored.LastSuccessAt.Equal(oldSuccess) {
		t.Fatalf("ambiguous response overwrote last success: %#v", stored)
	}
}

func TestAutoRateAsyncRefreshUpdatesStaleCache(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldNow := rateNow
	rateNow = func() time.Time { return now }
	t.Cleanup(func() { rateNow = oldNow })
	installSettingsGetter(t, map[string]string{
		"rate.mode":              RateModeAuto,
		"rate.api_url":           "https://rate.example.test",
		"rate.cache_ttl_seconds": "10",
	})
	oldSuccess := now.Add(-time.Minute)
	store := installRateCacheStore(t, RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14},
		LastSuccessAt: &oldSuccess,
		LastAttemptAt: &oldSuccess,
		LastRefreshOK: true,
	})
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.15}}`)),
			Request:    r,
		}, nil
	})

	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("stale rate = %v, want immediate 0.14", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		stored := store.rows["cny"]
		store.mu.Unlock()
		if stored.Rates["usdt"] == 0.15 && stored.LastRefreshOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("asynchronous success was not persisted: %#v", stored)
		}
		time.Sleep(time.Millisecond)
	}
	if got := GetRateForCoin("usdt", "cny"); got != 0.15 {
		t.Fatalf("refreshed rate = %v, want 0.15", got)
	}
}

func TestAutoRatePartialPayloadPreservesRequiredCoinFallback(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldNow := rateNow
	rateNow = func() time.Time { return now }
	t.Cleanup(func() { rateNow = oldNow })
	installSettingsGetter(t, map[string]string{
		"rate.mode":              RateModeAuto,
		"rate.api_url":           "https://rate.example.test",
		"rate.cache_ttl_seconds": "10",
	})
	oldSuccess := now.Add(-time.Minute)
	store := installRateCacheStore(t, RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14},
		LastSuccessAt: &oldSuccess,
		LastAttemptAt: &oldSuccess,
		LastRefreshOK: true,
	})
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"btc":0.000001}}`)),
			Request:    r,
		}, nil
	})

	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("initial stale fallback = %v, want 0.14", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		stored := cloneSnapshot(store.rows["cny"])
		store.mu.Unlock()
		if stored.LastAttemptAt != nil && !stored.LastRefreshOK {
			if stored.Rates["usdt"] != 0.14 || stored.Rates["btc"] != 0 {
				t.Fatalf("partial payload replaced last usable cache: %#v", stored)
			}
			if !strings.Contains(stored.LastError, "no positive cny.usdt rate") {
				t.Fatalf("partial payload error = %q", stored.LastError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("partial refresh result was not persisted: %#v", stored)
		}
		time.Sleep(time.Millisecond)
	}
	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("fallback after partial payload = %v, want 0.14", got)
	}
}

func TestManualPartialRefreshMergesPreviouslySuccessfulCoins(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldSuccess := now.Add(-time.Minute)
	store := installRateCacheStore(t, RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14, "trx": 1.8},
		LastSuccessAt: &oldSuccess,
		LastAttemptAt: &oldSuccess,
		LastRefreshOK: true,
	})
	installSettingsGetter(t, map[string]string{
		"rate.mode":    RateModeAuto,
		"rate.api_url": "https://rate.example.test",
	})
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.15}}`)),
			Request:    r,
		}, nil
	})

	result := RefreshRateBase("cny", true)
	if !result.OK || result.Rates["usdt"] != 0.15 || result.Rates["trx"] != 1.8 {
		t.Fatalf("partial manual refresh result = %#v", result)
	}
	store.mu.Lock()
	stored := cloneSnapshot(store.rows["cny"])
	store.mu.Unlock()
	if stored.Rates["usdt"] != 0.15 || stored.Rates["trx"] != 1.8 {
		t.Fatalf("merged persisted rates = %#v", stored.Rates)
	}
}

func TestSuccessfulFetchIsNotPublishedWhenPersistenceFails(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldSuccess := now.Add(-time.Minute)
	oldLoad, oldLoadAll, oldSave := RateCacheLoad, RateCacheLoadAll, RateCacheSave
	RateCacheLoad = func(base string) (RateCacheSnapshot, error) {
		return RateCacheSnapshot{
			Base:          base,
			Rates:         map[string]float64{"usdt": 0.14},
			LastSuccessAt: &oldSuccess,
			LastAttemptAt: &oldSuccess,
			LastRefreshOK: true,
		}, nil
	}
	RateCacheLoadAll = nil
	RateCacheSave = func(RateCacheSnapshot) error { return errors.New("database unavailable") }
	ResetRateCacheRuntime()
	t.Cleanup(func() {
		RateCacheLoad, RateCacheLoadAll, RateCacheSave = oldLoad, oldLoadAll, oldSave
		ResetRateCacheRuntime()
	})
	installSettingsGetter(t, map[string]string{
		"rate.mode":    RateModeAuto,
		"rate.api_url": "https://rate.example.test",
	})
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.15}}`)),
			Request:    r,
		}, nil
	})

	result := RefreshRateBase("cny", true)
	if result.OK || !strings.Contains(result.Error, "persist rate cache") {
		t.Fatalf("persistence failure result = %#v", result)
	}
	if result.Rates["usdt"] != 0.14 {
		t.Fatalf("failed persistence published new result: %#v", result.Rates)
	}
	if got := GetRateForCoin("usdt", "cny"); got != 0.14 {
		t.Fatalf("runtime rate after persistence failure = %v, want durable 0.14", got)
	}
}

func TestStatusLoadDoesNotOverwriteNewerMemorySnapshot(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	oldAttempt := now.Add(-time.Minute)
	oldSnapshot := RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.14},
		LastSuccessAt: &oldAttempt,
		LastAttemptAt: &oldAttempt,
		LastRefreshOK: true,
	}
	newSnapshot := RateCacheSnapshot{
		Base:          "cny",
		Rates:         map[string]float64{"usdt": 0.15},
		LastSuccessAt: &now,
		LastAttemptAt: &now,
		LastRefreshOK: true,
	}
	oldLoad, oldLoadAll, oldSave := RateCacheLoad, RateCacheLoadAll, RateCacheSave
	loaded := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	RateCacheLoad = nil
	RateCacheLoadAll = func() ([]RateCacheSnapshot, error) {
		once.Do(func() { close(loaded) })
		<-release
		return []RateCacheSnapshot{oldSnapshot}, nil
	}
	RateCacheSave = func(RateCacheSnapshot) error { return nil }
	ResetRateCacheRuntime()
	t.Cleanup(func() {
		RateCacheLoad, RateCacheLoadAll, RateCacheSave = oldLoad, oldLoadAll, oldSave
		ResetRateCacheRuntime()
	})
	installSettingsGetter(t, map[string]string{
		"rate.mode":              RateModeAuto,
		"rate.cache_ttl_seconds": "300",
	})

	done := make(chan struct{})
	go func() {
		_ = listRateCacheSnapshots()
		close(done)
	}()
	<-loaded
	if err := storeRateCacheSnapshot(newSnapshot); err != nil {
		t.Fatalf("store new snapshot: %v", err)
	}
	close(release)
	<-done
	snapshot, found := loadRateCacheSnapshot("cny")
	if !found {
		t.Fatal("newer memory snapshot is missing")
	}
	if got := rateFromSnapshot(snapshot, "usdt"); got != 0.15 {
		t.Fatalf("status load overwrote newer memory rate: got %v, want 0.15", got)
	}
}

func TestAutoRateColdStartRequestsAreSingleflighted(t *testing.T) {
	installSettingsGetter(t, map[string]string{
		"rate.mode":    RateModeAuto,
		"rate.api_url": "https://rate.example.test",
	})
	installRateCacheStore(t)
	var calls atomic.Int32
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.15}}`)),
			Request:    r,
		}, nil
	})

	var wg sync.WaitGroup
	results := make(chan float64, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- GetRateForCoin("usdt", "cny")
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result != 0.15 {
			t.Fatalf("concurrent rate = %v, want 0.15", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent API calls = %d, want 1", calls.Load())
	}
}
