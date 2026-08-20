package kvm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadISOProgress(t *testing.T) {
	body := make([]byte, 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := dir + "/test.iso"
	var lastDone, lastTotal int64
	err := downloadISO(context.Background(), srv.URL, dest, func(done, total int64) {
		lastDone = done
		lastTotal = total
	})
	if err != nil {
		t.Fatal(err)
	}
	if lastTotal != int64(len(body)) {
		t.Fatalf("total = %d, want %d", lastTotal, len(body))
	}
	if lastDone != int64(len(body)) {
		t.Fatalf("done = %d, want %d", lastDone, len(body))
	}
}

func TestMediaActivityPercent(t *testing.T) {
	a := MediaActivity{Done: 25, Total: 100}
	if a.Percent() != 25 {
		t.Fatalf("Percent() = %d, want 25", a.Percent())
	}
}
