package kvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CacheEntry describes a URL-downloaded ISO retained on disk.
type CacheEntry struct {
	URL         string
	Label       string
	Size        int64
	RetainUntil time.Time // zero while mounted
	InUse       bool
}

// List returns retained cache entries (including in-use mounts).
func (c *MediaCache) List() ([]CacheEntry, error) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return nil, err
	}
	_ = c.PurgeExpired()

	c.mu.Lock()
	defer c.mu.Unlock()

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CacheEntry
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
		if !meta.RetainUntil.IsZero() && now.After(meta.RetainUntil) {
			continue
		}
		isoPath := strings.TrimSuffix(metaPath, cacheMetaSuffix)
		st, err := os.Stat(isoPath)
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, CacheEntry{
			URL:         meta.URL,
			Label:       urlLabel(meta.URL),
			Size:        st.Size(),
			RetainUntil: meta.RetainUntil,
			InUse:       meta.RetainUntil.IsZero(),
		})
	}
	return out, nil
}

// PurgeEntry removes a cached ISO and metadata for url.
func (c *MediaCache) PurgeEntry(url string) error {
	key, err := cacheKey(url)
	if err != nil {
		return err
	}
	isoPath := filepath.Join(c.dir, key+".iso")
	metaPath := isoPath + cacheMetaSuffix

	c.mu.Lock()
	defer c.mu.Unlock()

	if meta, err := readCacheMeta(metaPath); err == nil && meta.RetainUntil.IsZero() {
		return fmt.Errorf("media cache: %q is in use — unmount first", urlLabel(url))
	}
	_ = os.Remove(isoPath)
	_ = os.Remove(metaPath)
	return nil
}
