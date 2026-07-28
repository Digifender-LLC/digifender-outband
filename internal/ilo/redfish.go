package ilo

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// redfishClient talks HTTPS Redfish/REST to an HPE iLO (Basic auth).
type redfishClient struct {
	baseURL  string
	user     string
	password string
	http     *http.Client
}

func newRedfish(cfg Config) *redfishClient {
	port := cfg.Port
	if port == 0 {
		port = 443
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // iLO commonly uses self-signed certs
		},
		MaxIdleConns:    2,
		IdleConnTimeout: 60 * time.Second,
	}
	return &redfishClient{
		baseURL:  fmt.Sprintf("https://%s:%d", cfg.Host, port),
		user:     cfg.User,
		password: cfg.Password,
		http: &http.Client{
			Transport: transport,
			Timeout:   45 * time.Second,
			// Follow redirects so bare /Systems/1 → /Systems/1/ works if a proxy strips the slash.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func ensureTrailingSlash(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}

func (c *redfishClient) url(path string) string {
	return c.baseURL + ensureTrailingSlash(path)
}

func (c *redfishClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func (c *redfishClient) getJSON(ctx context.Context, path string, dest any) error {
	data, code, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("redfish GET %s: HTTP %d: %s", ensureTrailingSlash(path), code, truncateErr(data))
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("redfish GET %s: decode: %w", ensureTrailingSlash(path), err)
	}
	return nil
}

func (c *redfishClient) postJSON(ctx context.Context, path string, body any) error {
	data, code, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	// iLO often returns 200 or 204; some firmwares use 202.
	if code < 200 || code >= 300 {
		return fmt.Errorf("redfish POST %s: HTTP %d: %s", ensureTrailingSlash(path), code, truncateErr(data))
	}
	return nil
}

func truncateErr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
