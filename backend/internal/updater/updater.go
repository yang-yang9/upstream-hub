package updater

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/worryzyy/upstream-hub/internal/version"
)

const (
	cacheTTL        = 10 * time.Minute
	maxDownloadSize = 200 * 1024 * 1024 // 200 MB
	httpTimeout     = 30 * time.Second
	downloadTimeout = 10 * time.Minute
)

type ReleaseInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	Changelog      string `json:"changelog"`
	PublishedAt    string `json:"published_at"`
	HTMLURL        string `json:"html_url"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type Updater struct {
	repo        string
	proxy       string
	httpClient  *http.Client
	dlClient    *http.Client
	log         *slog.Logger

	mu       sync.Mutex
	cached   *ReleaseInfo
	release  *githubRelease
	cachedAt time.Time
}

func New(repo, proxy string, log *slog.Logger) *Updater {
	return &Updater{
		repo:       repo,
		proxy:      strings.TrimRight(proxy, "/"),
		httpClient: &http.Client{Timeout: httpTimeout},
		dlClient:   &http.Client{Timeout: downloadTimeout},
		log:        log,
	}
}

func (u *Updater) CheckLatest(ctx context.Context) (*ReleaseInfo, error) {
	u.mu.Lock()
	if u.cached != nil && time.Since(u.cachedAt) < cacheTTL {
		info := *u.cached
		u.mu.Unlock()
		return &info, nil
	}
	u.mu.Unlock()

	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(version.Version, "v")

	info := &ReleaseInfo{
		CurrentVersion: version.Version,
		LatestVersion:  release.TagName,
		HasUpdate:      compareVersions(current, latest) < 0,
		Changelog:      release.Body,
		PublishedAt:    release.PublishedAt,
		HTMLURL:        release.HTMLURL,
	}

	u.mu.Lock()
	u.cached = info
	u.release = release
	u.cachedAt = time.Now()
	u.mu.Unlock()

	return info, nil
}

func (u *Updater) Perform(ctx context.Context) error {
	u.mu.Lock()
	release := u.release
	u.mu.Unlock()

	if release == nil {
		info, err := u.CheckLatest(ctx)
		if err != nil {
			return err
		}
		if !info.HasUpdate {
			return fmt.Errorf("already up to date")
		}
		u.mu.Lock()
		release = u.release
		u.mu.Unlock()
	}

	archiveName := fmt.Sprintf("upstream-hub-linux-%s.tar.gz", runtime.GOARCH)
	var downloadURL, checksumURL string

	for _, asset := range release.Assets {
		if asset.Name == archiveName {
			downloadURL = asset.BrowserDownloadURL
		}
		if asset.Name == "checksums.txt" {
			checksumURL = asset.BrowserDownloadURL
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no compatible release asset found for linux/%s", runtime.GOARCH)
	}

	downloadURL = u.applyProxy(downloadURL)
	if checksumURL != "" {
		checksumURL = u.applyProxy(checksumURL)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	tempDir, err := os.MkdirTemp(exeDir, ".upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	u.log.Info("downloading update", "url", downloadURL)
	archivePath := filepath.Join(tempDir, archiveName)
	if err := u.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if checksumURL != "" {
		u.log.Info("verifying checksum")
		if err := u.verifyChecksum(ctx, archivePath, checksumURL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	u.log.Info("extracting binary")
	newBinaryPath := filepath.Join(tempDir, "upstream-hub")
	if err := extractBinary(archivePath, newBinaryPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	u.log.Info("replacing binary", "path", exePath)
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		return fmt.Errorf("replace binary failed: %w", err)
	}

	u.mu.Lock()
	u.cached = nil
	u.release = nil
	u.mu.Unlock()

	u.log.Info("upgrade complete, restart required")
	return nil
}

func (u *Updater) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.repo)
	if u.proxy != "" {
		apiURL = u.proxy + "/" + apiURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "upstream-hub-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &release, nil
}

func (u *Updater) downloadFile(ctx context.Context, rawURL, dest string) error {
	if err := validateDownloadURL(rawURL, u.proxy); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}

	resp, err := u.dlClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	if resp.ContentLength > maxDownloadSize {
		return fmt.Errorf("file too large: %d bytes", resp.ContentLength)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}

	limited := io.LimitReader(resp.Body, maxDownloadSize+1)
	written, err := io.Copy(out, limited)
	_ = out.Close()

	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if written > maxDownloadSize {
		_ = os.Remove(dest)
		return fmt.Errorf("download exceeded max size %d bytes", maxDownloadSize)
	}

	return nil
}

func (u *Updater) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch checksums: HTTP %d", resp.StatusCode)
	}

	checksumData, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(strings.NewReader(string(checksumData)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) >= 2 && parts[1] == fileName {
			if parts[0] == actualHash {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
		}
	}

	return fmt.Errorf("checksum not found for %s", fileName)
}

func (u *Updater) applyProxy(rawURL string) string {
	if u.proxy == "" {
		return rawURL
	}
	return u.proxy + "/" + rawURL
}

func validateDownloadURL(rawURL, proxy string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("only HTTP(S) URLs are allowed")
	}

	host := parsed.Host
	if proxy != "" {
		proxyParsed, _ := url.Parse(proxy)
		if proxyParsed != nil && host == proxyParsed.Host {
			return nil
		}
	}

	allowed := []string{"github.com", "objects.githubusercontent.com"}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return nil
		}
	}

	return fmt.Errorf("download from untrusted host: %s", host)
}

func extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("path traversal detected: %s", hdr.Name)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		baseName := filepath.Base(hdr.Name)
		if baseName == "upstream-hub" {
			if hdr.Size > maxDownloadSize {
				return fmt.Errorf("binary too large: %d bytes", hdr.Size)
			}

			out, err := os.Create(destPath)
			if err != nil {
				return err
			}

			limited := io.LimitReader(tr, maxDownloadSize)
			if _, err := io.Copy(out, limited); err != nil {
				_ = out.Close()
				return err
			}
			return out.Close()
		}
	}

	return fmt.Errorf("binary 'upstream-hub' not found in archive")
}

func compareVersions(a, b string) int {
	aParts := parseVersion(a)
	bParts := parseVersion(b)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		fmt.Sscanf(parts[i], "%d", &result[i])
	}
	return result
}
