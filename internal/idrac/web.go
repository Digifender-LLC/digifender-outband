package idrac

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"outband/internal/bmc"
)

// webBackend talks to the classic iDRAC6/7 embedded web UI data API
// (POST data/login, POST data?get=… / data?set=…).
type webBackend struct {
	cfg     Config
	baseURL string
	http    *http.Client

	mu     sync.Mutex
	logged bool
	st2    string
}

var (
	reAuthResult  = regexp.MustCompile(`(?i)<authResult>([^<]+)</authResult>`)
	reForwardURL  = regexp.MustCompile(`(?i)<forwardUrl>([^<]+)</forwardUrl>`)
	reST2Query    = regexp.MustCompile(`(?i)ST2=([0-9a-f]+)`)
	reST2Var      = regexp.MustCompile(`(?i)TOKEN_VALUE\s*=\s*"([0-9a-f]+)"`)
	reStatusOK    = regexp.MustCompile(`(?i)<status>\s*ok\s*</status>`)
	reXMLTag      = func(tag string) *regexp.Regexp {
		return regexp.MustCompile(`(?is)<` + tag + `>(.*?)</` + tag + `>`)
	}
)

func newWebBackend(cfg Config, useLegacyTLS bool) *webBackend {
	tlsCfg := modernTLS(cfg.InsecureSkipVerify)
	if useLegacyTLS {
		tlsCfg = legacyTLS(cfg.InsecureSkipVerify)
	}
	return &webBackend{
		cfg:     cfg,
		baseURL: baseURL(cfg),
		http:    newHTTPClient(tlsCfg, true),
	}
}

func (c *webBackend) Name() string { return TransportWeb }

func (c *webBackend) Close() error {
	c.mu.Lock()
	logged := c.logged
	c.logged = false
	c.st2 = ""
	c.mu.Unlock()
	if logged {
		ctx := context.Background()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/data/logout", nil)
		if err == nil {
			c.setHeaders(req)
			if resp, err := c.http.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

func (c *webBackend) setHeaders(req *http.Request) {
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	c.mu.Lock()
	st2 := c.st2
	c.mu.Unlock()
	if st2 != "" {
		req.Header.Set("ST2", st2)
	}
}

func (c *webBackend) ensureLogin(ctx context.Context) error {
	c.mu.Lock()
	if c.logged {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.login(ctx)
}

func (c *webBackend) login(ctx context.Context) error {
	// Seed cookies / session like a browser.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/login.html", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()

	form := url.Values{}
	form.Set("user", c.cfg.User)
	form.Set("password", c.cfg.Password)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/data/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("idrac web login: HTTP %d: %s", resp.StatusCode, truncateErr(body))
	}
	m := reAuthResult.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("idrac web login: missing authResult")
	}
	if strings.TrimSpace(string(m[1])) != "0" {
		return fmt.Errorf("idrac web login failed (authResult=%s)", strings.TrimSpace(string(m[1])))
	}
	fwd := ""
	if m := reForwardURL.FindSubmatch(body); m != nil {
		fwd = strings.TrimSpace(string(m[1]))
		fwd = strings.ReplaceAll(fwd, "defaultCred", "index")
	}
	st2 := ""
	if m := reST2Query.FindStringSubmatch(fwd); len(m) > 1 {
		st2 = m[1]
	}
	if st2 == "" && fwd != "" {
		idxURL := fwd
		if !strings.HasPrefix(idxURL, "http") {
			if !strings.HasPrefix(idxURL, "/") {
				idxURL = "/" + idxURL
			}
			idxURL = c.baseURL + idxURL
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, idxURL, nil)
		if err == nil {
			c.setHeaders(req)
			if resp, err := c.http.Do(req); err == nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				resp.Body.Close()
				if m := reST2Var.FindSubmatch(b); m != nil {
					st2 = string(m[1])
				}
			}
		}
	}
	c.mu.Lock()
	c.logged = true
	c.st2 = st2
	c.mu.Unlock()
	return nil
}

func (c *webBackend) postData(ctx context.Context, path string) ([]byte, error) {
	if err := c.ensureLogin(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(""))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.logged = false
		c.mu.Unlock()
		if err := c.ensureLogin(ctx); err != nil {
			return nil, err
		}
		return c.postData(ctx, path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("idrac web %s: HTTP %d: %s", path, resp.StatusCode, truncateErr(body))
	}
	return body, nil
}

func (c *webBackend) getInfos(ctx context.Context, keys []string) (map[string]string, error) {
	body, err := c.postData(ctx, "/data?get="+strings.Join(keys, ","))
	if err != nil {
		return nil, err
	}
	if !reStatusOK.Match(body) {
		return nil, fmt.Errorf("idrac web get: status not ok: %s", truncateErr(body))
	}
	out := make(map[string]string, len(keys))
	s := string(body)
	for _, k := range keys {
		if m := reXMLTag(k).FindStringSubmatch(s); len(m) > 1 {
			out[k] = strings.TrimSpace(m[1])
		}
	}
	return out, nil
}

func (c *webBackend) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	info := &bmc.MCInfo{
		Manufacturer:    "Dell",
		Model:           "iDRAC",
		ProtocolVersion: "iDRAC-Web",
		FirmwareRev:     "unknown",
	}
	vals, err := c.getInfos(ctx, []string{"sysDesc", "fwVersion", "hwVersion", "svcTag", "hostName", "racName"})
	if err != nil {
		return nil, err
	}
	if v := vals["sysDesc"]; v != "" {
		info.Model = v
	} else if v := vals["racName"]; v != "" {
		info.Model = v
	}
	if v := vals["fwVersion"]; v != "" {
		info.FirmwareRev = v
	} else if v := vals["hwVersion"]; v != "" {
		info.FirmwareRev = v
	}
	if v := vals["svcTag"]; v != "" {
		info.ProtocolVersion = "iDRAC-Web ST=" + v
	}
	return info, nil
}

func (c *webBackend) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	vals, err := c.getInfos(ctx, []string{"pwState"})
	if err != nil {
		return nil, err
	}
	switch vals["pwState"] {
	case "1":
		return &bmc.PowerStatus{IsOn: true}, nil
	case "0":
		return &bmc.PowerStatus{IsOn: false}, nil
	default:
		return nil, fmt.Errorf("idrac web: unknown pwState %q", vals["pwState"])
	}
}

func (c *webBackend) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	// Moob / iDRAC7: 0 off, 1 on, 2 cycle, 3 warm reset, 5 graceful shutdown.
	var code int
	switch action {
	case bmc.PowerOn:
		code = 1
	case bmc.PowerOff:
		code = 0
	case bmc.PowerCycle:
		code = 2
	case bmc.PowerSoft:
		code = 5
	default:
		return fmt.Errorf("%w: power action %q", bmc.ErrUnsupported, action)
	}
	body, err := c.postData(ctx, fmt.Sprintf("/data?set=pwState:%d", code))
	if err != nil {
		return err
	}
	if len(body) > 0 && !reStatusOK.Match(body) && !bytesContainsOK(body) {
		// Some firmwares return empty 200 on set.
		if len(bytes.TrimSpace(body)) > 0 {
			return fmt.Errorf("idrac web power: %s", truncateErr(body))
		}
	}
	return nil
}

func bytesContainsOK(b []byte) bool {
	return strings.Contains(strings.ToLower(string(b)), "ok")
}

func (c *webBackend) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	// Classic web API exposes limited scalars; synthesize identity sensors.
	vals, err := c.getInfos(ctx, []string{"sysDesc", "fwVersion", "hostName", "pwState"})
	if err != nil {
		return nil, err
	}
	out := make([]bmc.Sensor, 0, 3)
	if v := vals["sysDesc"]; v != "" {
		out = append(out, bmc.Sensor{
			ID: "sys-desc", Name: "System", Type: "Identity",
			Value: v, Status: "ok", Present: true,
		})
	}
	if v := vals["fwVersion"]; v != "" {
		out = append(out, bmc.Sensor{
			ID: "fw-version", Name: "iDRAC Firmware", Type: "Firmware",
			Value: v, Status: "ok", Present: true,
		})
	}
	if v := vals["pwState"]; v != "" {
		label := "unknown"
		if v == "1" {
			label = "On"
		} else if v == "0" {
			label = "Off"
		}
		out = append(out, bmc.Sensor{
			ID: "pw-state", Name: "Power State", Type: "Power",
			Value: label, Status: "ok", Present: true,
		})
	}
	if len(out) == 0 {
		out = append(out, bmc.Sensor{
			ID: "web", Name: "iDRAC Web", Type: "Health",
			Value: "ok", Status: "ok", Present: true,
		})
	}
	return out, nil
}

func (c *webBackend) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	// SEL via the classic data API is firmware-specific; return empty rather than fail the UI.
	_ = limit
	return []bmc.SELEntry{}, nil
}
