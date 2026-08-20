package kvm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestMediaCacheRetainAndReuse(t *testing.T) {
	body := []byte("cached-iso-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := NewMediaCache(dir, time.Hour)

	ctx := context.Background()
	path1, release1, err := c.GetOrDownload(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path1); err != nil {
		t.Fatal(err)
	}
	release1()

	path2, release2, err := c.GetOrDownload(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path1 {
		t.Fatalf("paths differ: %q vs %q", path1, path2)
	}
	release2()

	// After release, meta should have retain_until in the future.
	metaPath := path1 + cacheMetaSuffix
	meta, err := readCacheMeta(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if meta.RetainUntil.Before(time.Now()) {
		t.Fatalf("retain_until=%v should be in the future", meta.RetainUntil)
	}
}
