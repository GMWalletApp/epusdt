package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/version"
)

func TestSelectAssetsForCurrentPlatform(t *testing.T) {
	suffix := assetSuffix()
	assets := []version.Asset{
		{Name: "SHA256SUMS", DownloadURL: "https://github.com/checksums"},
		{Name: "epusdt-2.1.1-" + suffix, DownloadURL: "https://github.com/archive"},
		{Name: "epusdt-2.1.1-other-arch.tar.gz", DownloadURL: "https://github.com/other"},
	}
	archiveURL, checksumURL, name, err := selectAssets(assets)
	if err != nil {
		t.Fatal(err)
	}
	if archiveURL != "https://github.com/archive" || checksumURL != "https://github.com/checksums" || name != "epusdt-2.1.1-"+suffix {
		t.Fatalf("selected assets: %q %q %q", archiveURL, checksumURL, name)
	}
}

func TestValidateDownloadURL(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "github release", raw: "https://github.com/acme/epusdt/releases/download/v2.1.1/epusdt.tar.gz", want: true},
		{name: "asset host", raw: "https://release-assets.githubusercontent.com/download", want: true},
		{name: "http rejected", raw: "http://github.com/acme/epusdt/archive", want: false},
		{name: "unknown host rejected", raw: "https://example.com/archive", want: false},
		{name: "nonstandard port rejected", raw: "https://github.com:8443/archive", want: false},
		{name: "credentials rejected", raw: "https://user@github.com/archive", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			err = validateDownloadURL(parsed)
			if (err == nil) != tt.want {
				t.Fatalf("validateDownloadURL(%q) error=%v, want success=%v", tt.raw, err, tt.want)
			}
		})
	}
}

func TestParseSystemdUnit(t *testing.T) {
	for _, tt := range []struct {
		name        string
		cgroup      string
		wantUnit    string
		wantUserMgr bool
	}{
		{
			name:     "system service",
			cgroup:   "0::/system.slice/epusdt.service\n",
			wantUnit: "epusdt.service",
		},
		{
			name:        "user service uses leaf unit",
			cgroup:      "0::/user.slice/user-1000.slice/user@1000.service/app.slice/epusdt-update.service\n",
			wantUnit:    "epusdt-update.service",
			wantUserMgr: true,
		},
		{
			name:     "no service",
			cgroup:   "0::/user.slice/user-1000.slice/session-1.scope\n",
			wantUnit: "",
		},
		{
			name:     "first line without service then system service",
			cgroup:   "0::/user.slice/user-1000.slice/session-1.scope\n1::/system.slice/epusdt.service\n",
			wantUnit: "epusdt.service",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unit, userManager := parseSystemdUnit(tt.cgroup)
			if unit != tt.wantUnit || userManager != tt.wantUserMgr {
				t.Fatalf("parseSystemdUnit(%q) = (%q, %v), want (%q, %v)", tt.cgroup, unit, userManager, tt.wantUnit, tt.wantUserMgr)
			}
		})
	}
}

func TestExtractBinaryFromArchive(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "epusdt.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	data := []byte("binary")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "epusdt", Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "epusdt.new")
	if err := extractBinary(archivePath, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("extracted data=%q, want %q", got, data)
	}
}

func TestDetectBlocksSourceBuild(t *testing.T) {
	original := config.BuildType
	originalVersion := config.BuildVersion
	config.BuildType = "source"
	config.BuildVersion = "0.0.0-dev"
	defer func() { config.BuildType = original }()
	defer func() { config.BuildVersion = originalVersion }()
	capability := Detect()
	if capability.CanUpdate || (capability.BlockReason != BlockSourceBuild && capability.BlockReason != BlockContainer && capability.BlockReason != BlockUnsupportedOS) {
		t.Fatalf("capability=%+v", capability)
	}
}

func TestDetectBlocksSourceBuildWithValidVersion(t *testing.T) {
	originalBuildType := config.BuildType
	originalVersion := config.BuildVersion
	config.BuildType = "source"
	config.BuildVersion = "v2.1.0"
	defer func() {
		config.BuildType = originalBuildType
		config.BuildVersion = originalVersion
	}()

	capability := Detect()
	if capability.CanUpdate || (capability.BlockReason != BlockSourceBuild && capability.BlockReason != BlockContainer && capability.BlockReason != BlockUnsupportedOS) {
		t.Fatalf("capability=%+v", capability)
	}
}

func TestCapabilityForReleaseBlocksMissingPlatformAsset(t *testing.T) {
	original := config.BuildType
	config.BuildType = "release"
	defer func() { config.BuildType = original }()
	manager := NewManager(nil, config.GetAppVersion)
	manager.fetchLatest = func(context.Context) (*version.Release, error) { return nil, nil }
	capability := manager.CapabilityForRelease(&version.Release{Assets: []version.Asset{{Name: "SHA256SUMS", DownloadURL: "https://github.com/checksums"}}})
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" && runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return
	}
	if capability.CanUpdate {
		t.Fatalf("capability=%+v", capability)
	}
}

func TestSwapBinaryAndMarkHealthy(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "epusdt")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "epusdt.new")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(execPath, newPath, "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil || string(got) != "new" {
		t.Fatalf("new binary=%q err=%v", got, err)
	}
	if _, err := os.Stat(execPath + ".update-backup"); err != nil {
		t.Fatal("expected backup before health confirmation")
	}
	if err := swapBinary(execPath, newPath, "v2.1.2"); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("second swap before restart: %v", err)
	}
	markHealthy(execPath)
	if _, err := os.Stat(execPath + ".update-backup"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup after health confirmation err=%v, want file removed", err)
	}
	if _, err := os.Stat(execPath + ".update-pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending marker after health confirmation err=%v, want file removed", err)
	}
}

func TestRestorePreviousBinaryAfterFailedStartup(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "epusdt")
	backupPath := execPath + ".update-backup"
	pendingPath := execPath + ".update-pending"
	if err := os.WriteFile(execPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingPath, []byte("v2.1.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := rollbackPendingUpdate(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("rollback was not performed")
	}
	got, err := os.ReadFile(execPath)
	if err != nil || string(got) != "old" {
		t.Fatalf("restored binary=%q err=%v, want old binary", got, err)
	}
	if _, err := os.Stat(pendingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending marker err=%v, want file removed", err)
	}
}

type testUpdater func(context.Context, *version.Release, string, func(string, int)) error

func (f testUpdater) apply(ctx context.Context, release *version.Release, execPath string, report func(string, int)) error {
	return f(ctx, release, execPath, report)
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(func(context.Context) (*version.Release, error) {
		return &version.Release{TagName: "v2.1.1"}, nil
	}, func() string { return "v2.1.0" })
	m.restart = func() {}
	m.detect = func() Capability {
		return Capability{CanUpdate: true, CanRestart: true, ExecPath: filepath.Join(dir, "epusdt")}
	}
	return m
}

func waitForStatus(t *testing.T, manager *Manager, want string) State {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := manager.Status()
		if state.Status == want {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last state=%+v", want, manager.Status())
	return State{}
}

func TestManagerUpdateReportsProgressAndCompletes(t *testing.T) {
	manager := newTestManager(t)
	entered := make(chan struct{})
	finish := make(chan struct{})
	restarted := make(chan struct{}, 1)
	manager.restart = func() { restarted <- struct{}{} }
	manager.updater = testUpdater(func(_ context.Context, _ *version.Release, _ string, report func(string, int)) error {
		report(StageDownloading, 65)
		close(entered)
		<-finish
		return nil
	})
	if err := manager.Start(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	<-entered
	state := manager.Status()
	if state.Status != StatusRunning || state.Percent != 65 || state.TargetVersion != "v2.1.1" {
		t.Fatalf("running state=%+v", state)
	}
	close(finish)
	state = waitForStatus(t, manager, StatusSucceeded)
	if state.Stage != StageDone || state.Percent != 100 || !state.RestartRequired || state.TimeoutSeconds != int(downloadTimeout.Seconds()) {
		t.Fatalf("completed state=%+v", state)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("restart was not requested")
	}
}

func TestManagerFailureCanBeRetried(t *testing.T) {
	manager := newTestManager(t)
	manager.updater = testUpdater(func(context.Context, *version.Release, string, func(string, int)) error {
		return context.DeadlineExceeded
	})
	if err := manager.Start(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	failed := waitForStatus(t, manager, StatusFailed)
	if failed.Error != context.DeadlineExceeded.Error() {
		t.Fatalf("failed state error=%q", failed.Error)
	}
	manager.updater = testUpdater(func(context.Context, *version.Release, string, func(string, int)) error { return nil })
	if err := manager.Start(context.Background(), ""); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	state := waitForStatus(t, manager, StatusSucceeded)
	if state.Error != "" {
		t.Fatalf("retry retained prior error: %+v", state)
	}
}

func TestManagerRetryPublishesCheckingStateWhileFetchingRelease(t *testing.T) {
	manager := newTestManager(t)
	manager.restart = func() {}
	manager.mu.Lock()
	manager.state = State{Status: StatusFailed, Stage: StageDownloading, Error: "previous failure"}
	manager.mu.Unlock()

	fetchStarted := make(chan struct{})
	continueFetch := make(chan struct{})
	manager.fetchLatest = func(context.Context) (*version.Release, error) {
		close(fetchStarted)
		<-continueFetch
		return &version.Release{TagName: "v2.1.1"}, nil
	}
	manager.updater = testUpdater(func(context.Context, *version.Release, string, func(string, int)) error {
		return nil
	})
	startResult := make(chan error, 1)
	go func() { startResult <- manager.Start(context.Background(), "") }()

	<-fetchStarted
	state := manager.Status()
	if state.Status != StatusRunning || state.Stage != StageChecking || state.Error != "" {
		t.Fatalf("retry checking state=%+v", state)
	}
	close(continueFetch)
	if err := <-startResult; err != nil {
		t.Fatalf("retry start: %v", err)
	}
	state = waitForStatus(t, manager, StatusSucceeded)
	if state.TargetVersion != "v2.1.1" {
		t.Fatalf("completed target version=%q", state.TargetVersion)
	}
}

func TestChangedReleaseRequiresANewCheck(t *testing.T) {
	m := newTestManager(t)
	if err := m.Start(context.Background(), "v2.1.2"); !errors.Is(err, ErrReleaseChanged) {
		t.Fatalf("unexpected release change result: %v", err)
	}
}

type assetTransport func(*http.Request) (*http.Response, error)

func (fn assetTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestVerifiedReleaseCanBeAppliedAndRolledBack(t *testing.T) {
	image := []byte("test release image")
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	tw := tar.NewWriter(compressed)
	if err := tw.WriteHeader(&tar.Header{Name: "epusdt", Size: int64(len(image)), Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	asset := "epusdt-2.1.1-" + assetSuffix()
	release := &version.Release{TagName: "v2.1.1", Assets: []version.Asset{
		{Name: asset, DownloadURL: "https://github.com/archive"},
		{Name: "SHA256SUMS", DownloadURL: "https://github.com/checksums"},
	}}
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("checksum-valid-%t", valid), func(t *testing.T) {
			checksum := sha256.Sum256(archive.Bytes())
			if !valid {
				checksum[0] ^= 1
			}
			u := &updater{client: &http.Client{Transport: assetTransport(func(req *http.Request) (*http.Response, error) {
				data := archive.Bytes()
				if req.URL.Path == "/checksums" {
					data = []byte(fmt.Sprintf("%x  %s\n", checksum, asset))
				}
				return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}}
			path := filepath.Join(t.TempDir(), "epusdt")
			if err := os.WriteFile(path, []byte("previous"), 0755); err != nil {
				t.Fatal(err)
			}
			var stages []string
			err := u.apply(context.Background(), release, path, func(stage string, percent int) {
				if len(stages) == 0 || stages[len(stages)-1] != stage {
					stages = append(stages, stage)
				}
			})
			if valid {
				if err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, image) {
					t.Fatalf("installed image: %q %v", data, err)
				}
				if fmt.Sprint(stages) != "[downloading verifying extracting swapping]" {
					t.Fatalf("stages: %v", stages)
				}
				if restored, err := rollbackPendingUpdate(path); err != nil || !restored {
					t.Fatalf("rollback: %v %v", restored, err)
				}
			} else if !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("checksum rejection: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "previous" {
				t.Fatalf("original image: %q %v", data, err)
			}
		})
	}
}
