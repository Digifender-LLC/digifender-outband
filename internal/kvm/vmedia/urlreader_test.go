package vmedia

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLReaderReadAt(t *testing.T) {
	body := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		case http.MethodGet:
			ra := r.Header.Get("Range")
			if !strings.HasPrefix(ra, "bytes=") {
				http.Error(w, "no range", http.StatusBadRequest)
				return
			}
			var start, end int
			if _, err := fmt.Sscanf(ra, "bytes=%d-%d", &start, &end); err != nil {
				http.Error(w, "bad range", http.StatusBadRequest)
				return
			}
			if start < 0 || start >= len(body) {
				http.Error(w, "start out of range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if end >= len(body) {
				end = len(body) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start : end+1])
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	okSize, ok, err := SupportsRange(ctx, srv.URL)
	if err != nil || !ok || okSize != int64(len(body)) {
		t.Fatalf("SupportsRange() size=%d ok=%v err=%v", okSize, ok, err)
	}

	r, err := OpenURL(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var buf [4]byte
	n, err := r.ReadAt(buf[:], 4)
	if err != nil || n != 4 || string(buf[:]) != "4567" {
		t.Fatalf("ReadAt = %q n=%d err=%v", buf[:n], n, err)
	}
}

func TestNormalizeMediaURL(t *testing.T) {
	if _, err := normalizeMediaURL("ftp://x"); err == nil {
		t.Fatal("expected error for ftp")
	}
	got, err := normalizeMediaURL("https://example.com/a.iso")
	if err != nil || got != "https://example.com/a.iso" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
