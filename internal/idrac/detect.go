package idrac

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// resolveBackend picks a wire protocol for this host. Auto mode prefers Redfish
// when present (iDRAC8+/late 7), otherwise the legacy web data API (iDRAC6/7),
// then WS-MAN Basic. Forced transport skips probing other protocols.
func resolveBackend(ctx context.Context, cfg Config) (backend, error) {
	want := normalizeTransport(cfg.Transport)
	httpClient, legacy, err := probeHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	base := baseURL(cfg)

	switch want {
	case TransportRedfish:
		if ok, _ := probeRedfish(ctx, httpClient, base); !ok {
			return nil, fmt.Errorf("idrac: transport=redfish but /redfish/v1 is unavailable")
		}
		return newRedfishBackend(cfg, legacy), nil
	case TransportWeb:
		return newWebBackend(cfg, legacy), nil
	case TransportWSMAN:
		return newWSMANBackend(cfg, legacy), nil
	}

	// auto
	if ok, _ := probeRedfish(ctx, httpClient, base); ok {
		return newRedfishBackend(cfg, legacy), nil
	}
	if ok, _ := probeWebLogin(ctx, httpClient, base); ok {
		return newWebBackend(cfg, legacy), nil
	}
	if ok, _ := probeWSMAN(ctx, httpClient, base); ok {
		return newWSMANBackend(cfg, legacy), nil
	}
	return nil, fmt.Errorf("idrac: could not detect Redfish, web UI, or WS-MAN on %s", cfg.Host)
}

func probeRedfish(ctx context.Context, c *http.Client, base string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/redfish/v1", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	var root struct {
		RedfishVersion string `json:"RedfishVersion"`
		OdataID        string `json:"@odata.id"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return false, nil
	}
	return root.RedfishVersion != "" || strings.Contains(root.OdataID, "redfish"), nil
}

func probeWebLogin(ctx context.Context, c *http.Client, base string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/login.html", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return false, err
	}
	body := string(data)
	return strings.Contains(body, "iDRAC") ||
		strings.Contains(body, "Integrated Dell Remote Access") ||
		strings.Contains(body, "data/login"), nil
}

func probeWSMAN(ctx context.Context, c *http.Client, base string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/wsman", strings.NewReader(""))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	auth := resp.Header.Get("WWW-Authenticate")
	// iDRAC7: 401 Basic realm="OPENWSMAN"
	if resp.StatusCode == http.StatusUnauthorized && strings.Contains(strings.ToLower(auth), "basic") {
		return true, nil
	}
	// Some firmwares accept Identify without auth.
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	return false, nil
}
