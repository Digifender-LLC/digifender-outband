package vmedia

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const urlReadTimeout = 60 * time.Second

// URLReader serves an ISO over HTTP using Range requests (no full download).
type URLReader struct {
	url    string
	client *http.Client
	size   int64
	mu     sync.Mutex
}

// OpenURL probes the URL and returns a streaming Reader when the origin supports
// byte ranges and reports a stable Content-Length.
func OpenURL(ctx context.Context, rawURL string) (*URLReader, error) {
	u, err := normalizeMediaURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: urlReadTimeout}
	size, ok, err := probeRange(ctx, client, u)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("vmedia: URL does not support byte ranges (use cache delivery)")
	}
	if size <= 0 {
		return nil, fmt.Errorf("vmedia: URL has unknown or zero length")
	}
	return &URLReader{url: u, client: client, size: size}, nil
}

// SupportsRange reports whether rawURL can be streamed via HTTP Range reads.
func SupportsRange(ctx context.Context, rawURL string) (size int64, ok bool, err error) {
	u, err := normalizeMediaURL(rawURL)
	if err != nil {
		return 0, false, err
	}
	client := &http.Client{Timeout: urlReadTimeout}
	return probeRange(ctx, client, u)
}

func probeRange(ctx context.Context, client *http.Client, u string) (size int64, ok bool, err error) {
	head, err := client.Head(u)
	if err != nil {
		return 0, false, fmt.Errorf("vmedia: HEAD %s: %w", u, err)
	}
	defer head.Body.Close()
	if head.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("vmedia: HEAD %s: HTTP %d", u, head.StatusCode)
	}
	size = head.ContentLength
	if size <= 0 {
		return 0, false, nil
	}
	if !strings.Contains(strings.ToLower(head.Header.Get("Accept-Ranges")), "bytes") {
		// Some servers omit Accept-Ranges but still honour Range; probe anyway.
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("vmedia: range probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return size, false, nil
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if n := parseContentRangeTotal(cr); n > 0 {
			size = n
		}
	}
	return size, true, nil
}

func parseContentRangeTotal(v string) int64 {
	// bytes 0-1023/12345
	i := strings.LastIndex(v, "/")
	if i < 0 || i+1 >= len(v) {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (r *URLReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("vmedia: negative read offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= r.size {
		end = r.size - 1
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	req, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("vmedia: GET range: HTTP %d", resp.StatusCode)
	}
	n, err := io.ReadFull(resp.Body, p[:end-off+1])
	if err == io.ErrUnexpectedEOF {
		return n, io.EOF
	}
	return n, err
}

func (r *URLReader) Size() int64 { return r.size }

// Close is a no-op; the client has no persistent connection.
func (r *URLReader) Close() error { return nil }

func normalizeMediaURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("vmedia: empty URL")
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("vmedia: URL must be http or https")
	}
	return raw, nil
}
