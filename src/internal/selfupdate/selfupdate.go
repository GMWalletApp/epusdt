package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/version"
	"github.com/gofrs/flock"
)

const (
	DeploymentBinary    = "binary"
	DeploymentContainer = "container"
	SupervisorSystemd   = "systemd"
	SupervisorNone      = "none"
	BlockContainer      = "container"
	BlockUnsupportedOS  = "unsupported_os"
	BlockSourceBuild    = "source_build"
	BlockDirNotWritable = "dir_not_writable"
	BlockExecNotFound   = "exec_not_found"
	BlockAssetNotFound  = "asset_not_found"
	StageIdle           = "idle"
	StageChecking       = "checking"
	StageDownloading    = "downloading"
	StageVerifying      = "verifying"
	StageExtracting     = "extracting"
	StageSwapping       = "swapping"
	StageDone           = "done"
	StatusIdle          = "idle"
	StatusRunning       = "running"
	StatusSucceeded     = "succeeded"
	StatusFailed        = "failed"
	restartExitCode     = 70
	maxArchiveSize      = 100 << 20
	maxBinarySize       = 200 << 20
	maxChecksumSize     = 1 << 20
	downloadTimeout     = 10 * time.Minute
)

var (
	ErrUpdateInProgress = errors.New("update already in progress")
	ErrNotSupported     = errors.New("self-update not supported in current environment")
	ErrReleaseChanged   = errors.New("release changed; check for updates again")
	ErrNoUpdate         = errors.New("no update available")
	ErrAssetNotFound    = errors.New("no release asset for current platform")
	ErrChecksumMismatch = errors.New("checksum mismatch")
	restartRequested    atomic.Bool
)

type Capability struct {
	CanUpdate   bool   `json:"can_update"`
	CanRestart  bool   `json:"can_restart"`
	BlockReason string `json:"block_reason,omitempty"`
	Deployment  string `json:"deployment"`
	Supervisor  string `json:"supervisor"`
	BuildType   string `json:"build_type"`
	ExecPath    string `json:"exec_path,omitempty"`
	Platform    string `json:"platform"`
	AssetName   string `json:"asset_name,omitempty"`
}

type State struct {
	Status          string     `json:"status"`
	Stage           string     `json:"stage"`
	Percent         int        `json:"percent"`
	TimeoutSeconds  int        `json:"timeout_seconds,omitempty"`
	TargetVersion   string     `json:"target_version,omitempty"`
	Error           string     `json:"error,omitempty"`
	RestartRequired bool       `json:"restart_required"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type Manager struct {
	mu          sync.Mutex
	state       State
	busy        bool
	fetchLatest func(context.Context) (*version.Release, error)
	current     func() string
	updater     updaterRunner
	detect      func() Capability
	restart     func()
}

type updaterRunner interface {
	apply(context.Context, *version.Release, string, func(string, int)) error
}

func NewManager(fetchLatest func(context.Context) (*version.Release, error), current func() string) *Manager {
	return &Manager{
		state:       State{Status: StatusIdle, Stage: StageIdle},
		fetchLatest: fetchLatest,
		current:     current,
		updater:     newUpdater(),
		detect:      Detect,
		restart:     RequestRestart,
	}
}

func (m *Manager) Capability() Capability { return m.detect() }

func (m *Manager) CapabilityForRelease(release *version.Release) Capability {
	capability := m.detect()
	if capability.CanUpdate && release != nil {
		if _, _, _, err := selectAssets(release.Assets); err != nil {
			capability.CanUpdate = false
			capability.BlockReason = BlockAssetNotFound
			capability.AssetName = ""
		}
	}
	return capability
}

func (m *Manager) Status() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) Start(ctx context.Context, expectedVersion string) error {
	capability := m.detect()
	if !capability.CanUpdate {
		return ErrNotSupported
	}
	m.mu.Lock()
	if m.busy || m.state.RestartRequired {
		m.mu.Unlock()
		return ErrUpdateInProgress
	}
	m.busy = true
	m.mu.Unlock()

	execPath := capability.ExecPath
	unlock, err := acquireLock(execPath)
	if err != nil {
		m.releaseBusy()
		return err
	}
	started := time.Now()
	m.mu.Lock()
	m.state = State{
		Status: StatusRunning, Stage: StageChecking,
		TimeoutSeconds: int(downloadTimeout.Seconds()),
		StartedAt:      &started,
	}
	m.mu.Unlock()

	release, err := m.fetchLatest(ctx)
	if err != nil {
		unlock()
		m.failStart(err)
		return err
	}
	if release == nil {
		err = errors.New("release checker returned no release")
		unlock()
		m.failStart(err)
		return err
	}
	if expectedVersion != "" && release.TagName != expectedVersion {
		unlock()
		m.failStart(ErrReleaseChanged)
		return ErrReleaseChanged
	}
	current := m.current()
	hasUpdate, err := version.IsNewerVersion(release.TagName, current)
	if err != nil {
		unlock()
		m.failStart(err)
		return err
	}
	if !hasUpdate {
		unlock()
		m.failStart(ErrNoUpdate)
		return ErrNoUpdate
	}
	m.mu.Lock()
	m.state.Stage = StageDownloading
	m.state.Percent = -1
	m.state.TargetVersion = release.TagName
	m.mu.Unlock()
	go m.run(release, execPath, capability.CanRestart, unlock)
	return nil
}

func (m *Manager) run(release *version.Release, execPath string, canRestart bool, unlock func()) {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	err := m.updater.apply(ctx, release, execPath, m.report)
	cancel()
	unlock()
	m.mu.Lock()
	finished := time.Now()
	m.state.FinishedAt = &finished
	m.busy = false
	if err != nil {
		m.state.Status = StatusFailed
		m.state.Error = err.Error()
		m.mu.Unlock()
		return
	}
	m.state.Status = StatusSucceeded
	m.state.Stage = StageDone
	m.state.Percent = 100
	m.state.RestartRequired = true
	m.mu.Unlock()
	if canRestart {
		m.restart()
	}
}

func (m *Manager) report(stage string, percent int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Stage, m.state.Percent = stage, percent
}

func (m *Manager) releaseBusy() {
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
}

func (m *Manager) failStart(err error) {
	finished := time.Now()
	m.mu.Lock()
	m.state.Status = StatusFailed
	m.state.Error = err.Error()
	m.state.FinishedAt = &finished
	m.busy = false
	m.mu.Unlock()
}

func Detect() Capability {
	capability := Capability{
		Deployment: DeploymentBinary,
		Supervisor: SupervisorNone,
		BuildType:  config.BuildType,
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
	}
	if config.BuildType == DeploymentContainer || inContainer() {
		capability.Deployment = DeploymentContainer
		capability.BlockReason = BlockContainer
		return capability
	}
	if runtime.GOOS == "windows" {
		capability.BlockReason = BlockUnsupportedOS
		return capability
	}
	if !strings.EqualFold(strings.TrimSpace(config.BuildType), "release") {
		capability.BlockReason = BlockSourceBuild
		return capability
	}
	if isDevelopmentBuild() {
		capability.BlockReason = BlockSourceBuild
		return capability
	}
	execPath, err := executablePath()
	if err != nil {
		capability.BlockReason = BlockExecNotFound
		return capability
	}
	capability.ExecPath = execPath
	if !dirWritable(filepath.Dir(execPath)) {
		capability.BlockReason = BlockDirNotWritable
		return capability
	}
	capability.CanUpdate = true
	capability.AssetName = assetSuffix()
	capability.Supervisor, capability.CanRestart = detectSupervisor()
	return capability
}

func isDevelopmentBuild() bool {
	current := strings.TrimSpace(config.GetAppVersion())
	if current == "" || strings.Contains(strings.ToLower(current), "dev") {
		return true
	}
	_, err := version.IsNewerVersion(current, current)
	return err != nil
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	data, _ := os.ReadFile("/proc/1/cgroup")
	for _, marker := range []string{"/docker/", "containerd", "kubepods", "libpod", "/lxc/"} {
		if strings.Contains(string(data), marker) {
			return true
		}
	}
	return false
}

func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".epusdt-update-check-")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func detectSupervisor() (string, bool) {
	if os.Getenv("INVOCATION_ID") == "" && os.Getenv("JOURNAL_STREAM") == "" {
		return SupervisorNone, false
	}
	unit, userManager := systemdUnit()
	if unit == "" {
		return SupervisorSystemd, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"show", unit, "-p", "Restart", "--value"}
	if userManager {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return SupervisorSystemd, false
	}
	restart := strings.TrimSpace(string(out))
	return SupervisorSystemd, restart == "always" || restart == "on-failure"
}

// systemdUnit returns the innermost service unit from the process cgroup and
// whether it belongs to a user manager. A user service cgroup contains the
// manager unit before the leaf, e.g.
// /user.slice/user-1000.slice/user@1000.service/app.slice/epusdt.service.
func systemdUnit() (string, bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	return parseSystemdUnit(string(data))
}

func parseSystemdUnit(cgroup string) (string, bool) {
	for _, line := range strings.Split(cgroup, "\n") {
		var unit string
		var userManager bool
		for _, part := range strings.Split(line, "/") {
			if !strings.HasSuffix(part, ".service") {
				continue
			}
			unit = part
			if strings.HasPrefix(part, "user@") {
				userManager = true
			}
		}
		if unit != "" {
			return unit, userManager
		}
	}
	return "", false
}

type updater struct{ client *http.Client }

func newUpdater() *updater {
	return &updater{client: &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return validateDownloadURL(req.URL)
		},
	}}
}

func (u *updater) apply(ctx context.Context, release *version.Release, execPath string, report func(string, int)) error {
	archiveURL, checksumURL, assetName, err := selectAssets(release.Assets)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(execPath), ".epusdt-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	report(StageDownloading, -1)
	archivePath := filepath.Join(tempDir, assetName)
	if err := u.download(ctx, archiveURL, archivePath, func(percent int) { report(StageDownloading, percent) }); err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	report(StageVerifying, 0)
	if err := u.verifyChecksum(ctx, archivePath, assetName, checksumURL); err != nil {
		return err
	}
	report(StageExtracting, 0)
	newBinary := filepath.Join(tempDir, "epusdt.new")
	if err := extractBinary(archivePath, newBinary); err != nil {
		return err
	}
	if err := os.Chmod(newBinary, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	report(StageSwapping, 0)
	if err := swapBinary(execPath, newBinary, release.TagName); err != nil {
		return err
	}
	return nil
}

var allowedHosts = map[string]bool{
	"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true,
}

func validateDownloadURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.User != nil || (value.Port() != "" && value.Port() != "443") || !allowedHosts[value.Hostname()] {
		return fmt.Errorf("untrusted update download host")
	}
	return nil
}

func selectAssets(assets []version.Asset) (string, string, string, error) {
	suffix := assetSuffix()
	var archiveURL, checksumURL, archiveName string
	for _, asset := range assets {
		if asset.Name == "SHA256SUMS" {
			checksumURL = asset.DownloadURL
		}
		if filepath.Base(asset.Name) == asset.Name && strings.HasPrefix(asset.Name, "epusdt-") && strings.HasSuffix(asset.Name, "-"+suffix) {
			archiveURL, archiveName = asset.DownloadURL, asset.Name
		}
	}
	if archiveURL == "" || checksumURL == "" {
		return "", "", "", ErrAssetNotFound
	}
	return archiveURL, checksumURL, archiveName, nil
}

func assetSuffix() string { return runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz" }

func (u *updater) download(ctx context.Context, rawURL, dest string, progress func(int)) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if err := validateDownloadURL(parsed); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "epusdt-self-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArchiveSize {
		return errors.New("update archive is too large")
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	var written int64
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxArchiveSize {
				return errors.New("update archive is too large")
			}
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			if resp.ContentLength > 0 && progress != nil {
				progress(int(written * 100 / resp.ContentLength))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return f.Sync()
}

func (u *updater) verifyChecksum(ctx context.Context, filePath, assetName, checksumURL string) error {
	parsed, err := url.Parse(checksumURL)
	if err != nil {
		return err
	}
	if err := validateDownloadURL(parsed); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "epusdt-self-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("checksum download returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumSize))
	if err != nil {
		return err
	}
	var expected string
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			expected = fields[0]
			break
		}
	}
	if len(expected) != 64 {
		return errors.New("archive checksum is missing")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return ErrChecksumMismatch
	}
	return nil
}

func extractBinary(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("epusdt binary not found in archive")
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "epusdt" {
			continue
		}
		if header.Size <= 0 || header.Size > maxBinarySize {
			return errors.New("extracted binary is too large")
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(out, tr, header.Size)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
			_ = os.Remove(dest)
			return errors.New("extract binary failed")
		}
		return nil
	}
}

func swapBinary(execPath, newBinary, targetVersion string) error {
	backup := execPath + ".update-backup"
	pending := execPath + ".update-pending"
	if _, err := os.Stat(pending); err == nil {
		return ErrUpdateInProgress
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep the live path executable throughout the swap. A hard link preserves
	// the previous image on the same filesystem for an atomic rollback.
	if err := os.Link(execPath, backup); err != nil {
		previous, statErr := os.Stat(backup)
		current, currentErr := os.Stat(execPath)
		if !errors.Is(err, os.ErrExist) || statErr != nil || currentErr != nil || !os.SameFile(previous, current) {
			return fmt.Errorf("backup current binary: %w", err)
		}
	}
	if err := os.WriteFile(pending+".tmp", []byte(targetVersion), 0o600); err != nil {
		return err
	}
	if err := os.Rename(pending+".tmp", pending); err != nil {
		_ = os.Remove(pending + ".tmp")
		return err
	}
	if err := os.Rename(newBinary, execPath); err != nil {
		_ = os.Remove(pending)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

func acquireLock(execPath string) (func(), error) {
	lock := flock.New(execPath + ".update-lock")
	acquired, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrUpdateInProgress
	}
	return func() { _ = lock.Unlock() }, nil
}

func MarkHealthy() {
	execPath, err := executablePath()
	if err != nil {
		return
	}
	markHealthy(execPath)
}

func markHealthy(execPath string) {
	if _, err := os.Stat(execPath + ".update-pending"); err != nil {
		return
	}
	_ = os.Remove(execPath + ".update-backup")
	_ = os.Remove(execPath + ".update-pending")
}

// RollbackPendingUpdate restores the previous executable when startup fails
// before the HTTP listener confirms the new binary is ready.
func RollbackPendingUpdate() (bool, error) {
	execPath, err := executablePath()
	if err != nil {
		return false, err
	}
	return rollbackPendingUpdate(execPath)
}

func rollbackPendingUpdate(execPath string) (bool, error) {
	pending := execPath + ".update-pending"
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	backup := execPath + ".update-backup"
	if _, err := os.Stat(backup); err != nil {
		return false, fmt.Errorf("update backup unavailable: %w", err)
	}
	if err := restorePreviousBinary(execPath); err != nil {
		return false, err
	}
	return true, nil
}

func restorePreviousBinary(execPath string) error {
	backup := execPath + ".update-backup"
	pending := execPath + ".update-pending"
	if err := os.Rename(backup, execPath); err != nil {
		return fmt.Errorf("restore previous executable: %w", err)
	}
	// Renaming two hard links to the same image is a no-op on POSIX.
	_ = os.Remove(backup)
	if err := os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove failed update marker: %w", err)
	}
	return nil
}

func RequestRestart() {
	restartRequested.Store(true)
	go func() {
		time.Sleep(500 * time.Millisecond)
		p, err := os.FindProcess(os.Getpid())
		if err != nil || p.Signal(syscall.SIGTERM) != nil {
			os.Exit(restartExitCode)
		}
	}()
}

func RestartRequested() bool { return restartRequested.Load() }

func RestartExitCode() int { return restartExitCode }
