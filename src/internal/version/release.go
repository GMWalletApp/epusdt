// Package version contains the server-side release checker used by the admin
// console. It deliberately has no dependency on the HTTP framework so the
// version comparison and GitHub client can be tested in isolation.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	defaultUserAgent     = "epusdt-update-checker"
	defaultCacheTTL      = time.Minute
)

var (
	ErrRateLimited = errors.New("github api rate limit exceeded")
	ErrNoRelease   = errors.New("github repository has no stable release")
)

type releasePayload struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []assetPayload `json:"assets"`
}

type assetPayload struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release is the small public subset needed by the admin response.
type Release struct {
	TagName     string
	Name        string
	Body        string
	PublishedAt time.Time
	Assets      []Asset
}

type Asset struct {
	Name        string
	DownloadURL string
}

type CheckResult struct {
	CurrentVersion string     `json:"current_version"`
	LatestVersion  string     `json:"latest_version"`
	HasUpdate      bool       `json:"has_update"`
	ReleaseURL     string     `json:"release_url,omitempty"`
	ReleaseName    string     `json:"release_name,omitempty"`
	ReleaseNotes   string     `json:"release_notes,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	Source         string     `json:"source"`
}

type cachedRelease struct {
	release *Release
	expires time.Time
}

type inFlight struct {
	done    chan struct{}
	release *Release
	err     error
}

// Checker caches successful release responses and joins concurrent requests.
// The cache is keyed by the server-side repository, never by a browser value.
type Checker struct {
	client  *http.Client
	apiBase string
	ttl     time.Duration
	now     func() time.Time

	mu       sync.Mutex
	cached   map[string]cachedRelease
	inFlight map[string]*inFlight
}

func NewChecker() *Checker {
	return NewCheckerWithClient(http.DefaultClient, defaultGitHubAPIBase, defaultCacheTTL)
}

// NewCheckerWithClient is also useful for deterministic tests with an
// httptest.Server. Production callers should use NewChecker.
func NewCheckerWithClient(client *http.Client, apiBase string, ttl time.Duration) *Checker {
	if client == nil {
		client = http.DefaultClient
	}
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		apiBase = defaultGitHubAPIBase
	}
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &Checker{
		client: client, apiBase: apiBase, ttl: ttl, now: time.Now,
		cached: make(map[string]cachedRelease), inFlight: make(map[string]*inFlight),
	}
}

// CheckLatestRelease fetches the latest stable release and compares it with
// current using SemVer. repository must be the server-configured owner/name.
func (c *Checker) CheckLatestRelease(ctx context.Context, current, repository string) (*CheckResult, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return nil, err
	}
	release, err := c.FetchLatestRelease(ctx, repository)
	if err != nil {
		return nil, err
	}
	latest := strings.TrimSpace(release.TagName)
	current = strings.TrimSpace(current)
	hasUpdate, err := IsNewerVersion(latest, current)
	if err != nil {
		return nil, fmt.Errorf("compare latest version %q with current %q: %w", latest, current, err)
	}
	result := &CheckResult{
		CurrentVersion: current,
		LatestVersion:  latest,
		HasUpdate:      hasUpdate,
		ReleaseURL:     releaseTagURL(repository, latest),
		ReleaseName:    release.Name,
		ReleaseNotes:   release.Body,
		Source:         fmt.Sprintf("https://github.com/%s/releases", repository),
	}
	if !release.PublishedAt.IsZero() {
		published := release.PublishedAt
		result.PublishedAt = &published
	}
	return result, nil
}

func releaseTagURL(repository, tag string) string {
	return "https://github.com/" + repository + "/releases/tag/" + url.PathEscape(tag)
}

func (c *Checker) FetchLatestRelease(ctx context.Context, repository string) (*Release, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return nil, err
	}
	now := c.now()
	c.mu.Lock()
	if item, ok := c.cached[repository]; ok && now.Before(item.expires) {
		release := cloneRelease(item.release)
		c.mu.Unlock()
		return release, nil
	}
	if call, ok := c.inFlight[repository]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return cloneRelease(call.release), call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &inFlight{done: make(chan struct{})}
	c.inFlight[repository] = call
	c.mu.Unlock()

	release, err := c.fetch(ctx, repository)
	c.mu.Lock()
	call.release, call.err = release, err
	if err == nil {
		c.cached[repository] = cachedRelease{release: cloneRelease(release), expires: c.now().Add(c.ttl)}
	}
	delete(c.inFlight, repository)
	close(call.done)
	c.mu.Unlock()
	return release, err
}

func (c *Checker) fetch(ctx context.Context, repository string) (*Release, error) {
	requestURL := c.apiBase + "/repos/" + repository + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request github api: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read github response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0") {
		return nil, ErrRateLimited
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload releasePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode github release: %w", err)
	}
	if payload.Draft || payload.Prerelease || strings.TrimSpace(payload.TagName) == "" {
		return nil, ErrNoRelease
	}
	release := &Release{
		TagName: payload.TagName, Name: payload.Name,
		Body: payload.Body, PublishedAt: payload.PublishedAt,
	}
	for _, asset := range payload.Assets {
		release.Assets = append(release.Assets, Asset{
			Name: asset.Name, DownloadURL: asset.BrowserDownloadURL,
		})
	}
	return release, nil
}

func cloneRelease(in *Release) *Release {
	if in == nil {
		return nil
	}
	out := *in
	out.Assets = append([]Asset(nil), in.Assets...)
	return &out
}

var repositoryPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func normalizeRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !repositoryPart.MatchString(parts[0]) || !repositoryPart.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid update repository")
	}
	return parts[0] + "/" + parts[1], nil
}

type semver struct {
	core       [3]string
	prerelease []string
}

func IsNewerVersion(latest, current string) (bool, error) {
	l, lErr := parseSemver(latest)
	c, cErr := parseSemver(current)
	if lErr != nil || cErr != nil {
		return false, errors.Join(lErr, cErr)
	}
	return compareSemver(l, c) > 0, nil
}

func compareSemver(a, b semver) int {
	for i := range 3 {
		if result := compareNumericIdentifiers(a.core[i], b.core[i]); result != 0 {
			return result
		}
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func comparePrerelease(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		aNum, bNum := isNumericIdentifier(a[i]), isNumericIdentifier(b[i])
		switch {
		case aNum && bNum:
			if result := compareNumericIdentifiers(a[i], b[i]); result != 0 {
				return result
			}
		case aNum != bNum:
			if aNum {
				return -1
			}
			return 1
		default:
			if result := strings.Compare(a[i], b[i]); result != 0 {
				return result
			}
		}
	}
	if len(a) > len(b) {
		return 1
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

func isNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNumericIdentifiers(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) > len(b) {
			return 1
		}
		return -1
	}
	return strings.Compare(a, b)
}

var semverIdentifier = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

func parseSemver(value string) (semver, error) {
	var out semver
	s := strings.TrimSpace(value)
	if s == "" {
		return out, errors.New("empty version")
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		build := s[i+1:]
		s = s[:i]
		if build == "" {
			return out, fmt.Errorf("empty build metadata in %s", value)
		}
		for _, id := range strings.Split(build, ".") {
			if id == "" || !semverIdentifier.MatchString(id) {
				return out, fmt.Errorf("invalid build metadata in %s", value)
			}
		}
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if pre == "" {
			return out, fmt.Errorf("empty prerelease in %s", value)
		}
		out.prerelease = strings.Split(pre, ".")
		for _, id := range out.prerelease {
			if id == "" || !semverIdentifier.MatchString(id) {
				return out, fmt.Errorf("invalid prerelease in %s", value)
			}
			if isNumericIdentifier(id) && len(id) > 1 && id[0] == '0' {
				return out, fmt.Errorf("numeric prerelease has leading zeros in %s", value)
			}
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("core version must have exactly 3 segments in %s", value)
	}
	for i, part := range parts {
		if !isNumericIdentifier(part) || (len(part) > 1 && part[0] == '0') {
			return out, fmt.Errorf("invalid version segment in %s", value)
		}
		out.core[i] = part
	}
	return out, nil
}
