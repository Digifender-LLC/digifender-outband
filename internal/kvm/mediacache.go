package kvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cacheMetaSuffix = ".meta.json"

type cacheMeta struct {
	URL         string    `json:"url"`
	RetainUntil time.Time `json:"retain_until"` // zero while mounted
}

// MediaCache stores downloaded ISOs under dir with a post-unmount retention window.
type MediaCache struct {
	dir string
	ttl time.Duration
	mu  sync.Mutex
}

// NewMediaCache prepares a cache directory (created if missing).
func NewMediaCache(dir string, ttl time.Duration) *MediaCache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &MediaCache{dir: dir, ttl: ttl}
}

// Dir returns the cache directory path.
func (c *MediaCache) Dir() string { return c.dir }

// PurgeExpired removes cache entries whose retention window has elapsed.
func (c *MediaCache) PurgeExpired() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.purgeExpiredLocked()
}

// GetOrDownload returns a cached ISO path for url, downloading when needed.
// The returned release func must run on unmount to start the retention timer.
func (c *MediaCache) GetOrDownload(ctx context.Context, url string) (path string, release func(), err error) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", nil, err
	}
	_ = c.PurgeExpired()

	key, err := cacheKey(url)
	if err != nil {
		return "", nil, err
	}
	isoPath := filepath.Join(c.dir, key+".iso")
	metaPath := isoPath + cacheMetaSuffix

	c.mu.Lock()
	defer c.mu.Unlock()

	if st, err := os.Stat(isoPath); err == nil && !st.IsDir() {
		if meta, err := readCacheMeta(metaPath); err == nil && meta.URL == url {
			if meta.RetainUntil.IsZero() || time.Now().Before(meta.RetainUntil) {
				if err := writeCacheMeta(metaPath, cacheMeta{URL: url, RetainUntil: time.Time{}}); err != nil {
					return "", nil, err
				}
				return isoPath, func() { c.release(url, isoPath, metaPath) }, nil
			}
		}
	}

	if err := downloadISO(ctx, url, isoPath); err != nil {
		return "", nil, err
	}
	if err := writeCacheMeta(metaPath, cacheMeta{URL: url, RetainUntil: time.Time{}}); err != nil {
		_ = os.Remove(isoPath)
		return "", nil, err
	}
	return isoPath, func() { c.release(url, isoPath, metaPath) }, nil
}

func (c *MediaCache) release(url, isoPath, metaPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until := time.Now().Add(c.ttl)
	_ = writeCacheMeta(metaPath, cacheMeta{URL: url, RetainUntil: until})
}

func (c *MediaCache) purgeExpiredLocked() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), cacheMetaSuffix) {
			continue
		}
		metaPath := filepath.Join(c.dir, e.Name())
		meta, err := readCacheMeta(metaPath)
		if err != nil {
			continue
		}
		if meta.RetainUntil.IsZero() || now.Before(meta.RetainUntil) {
			continue
		}
		isoPath := strings.TrimSuffix(metaPath, cacheMetaSuffix)
		_ = os.Remove(isoPath)
		_ = os.Remove(metaPath)
	}
	return nil
}

func downloadISO(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("media cache: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("media cache: download: HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cacheKey(url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("media cache: empty URL")
	}
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:16]), nil
}

func readCacheMeta(path string) (cacheMeta, error) {
	var m cacheMeta
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func writeCacheMeta(path string, m cacheMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
