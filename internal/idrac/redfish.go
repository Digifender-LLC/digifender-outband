package idrac

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"outband/internal/bmc"
)

// redfishBackend talks HTTPS Redfish with SessionService auth (iDRAC8/9/10, late iDRAC7).
type redfishBackend struct {
	cfg     Config
	baseURL string
	http    *http.Client

	mu        sync.Mutex
	token     string
	sessionID string
}

func newRedfishBackend(cfg Config, useLegacyTLS bool) *redfishBackend {
	tlsCfg := modernTLS(cfg.InsecureSkipVerify)
	if useLegacyTLS {
		tlsCfg = legacyTLS(cfg.InsecureSkipVerify)
	}
	return &redfishBackend{
		cfg:     cfg,
		baseURL: baseURL(cfg),
		http:    newHTTPClient(tlsCfg, false),
	}
}

func (c *redfishBackend) Name() string { return TransportRedfish }

func (c *redfishBackend) Close() error {
	c.logout(context.Background())
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (c *redfishBackend) url(path string) string {
	return c.baseURL + normalizePath(path)
}

func (c *redfishBackend) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	if c.token != "" {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.login(ctx)
}

func (c *redfishBackend) login(ctx context.Context) error {
	body := map[string]string{
		"UserName": c.cfg.User,
		"Password": c.cfg.Password,
	}
	data, code, hdr, err := c.doRaw(ctx, http.MethodPost, "/redfish/v1/SessionService/Sessions", body, false)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("redfish session login: HTTP %d: %s", code, truncateErr(data))
	}
	token := hdr.Get("X-Auth-Token")
	if token == "" {
		return fmt.Errorf("redfish session login: missing X-Auth-Token")
	}
	session := hdr.Get("Location")
	if session == "" {
		var resp struct {
			OdataID string `json:"@odata.id"`
			ID      string `json:"Id"`
		}
		_ = json.Unmarshal(data, &resp)
		session = resp.OdataID
		if session == "" && resp.ID != "" {
			session = "/redfish/v1/SessionService/Sessions/" + resp.ID
		}
	}
	c.mu.Lock()
	c.token = token
	c.sessionID = session
	c.mu.Unlock()
	return nil
}

func (c *redfishBackend) logout(ctx context.Context) {
	c.mu.Lock()
	token := c.token
	session := c.sessionID
	c.token = ""
	c.sessionID = ""
	c.mu.Unlock()
	if token == "" || session == "" {
		return
	}
	path := session
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		if strings.HasPrefix(path, c.baseURL) {
			path = strings.TrimPrefix(path, c.baseURL)
		} else {
			req, err := http.NewRequestWithContext(ctx, http.MethodDelete, path, nil)
			if err != nil {
				return
			}
			req.Header.Set("X-Auth-Token", token)
			req.Header.Set("Accept", "application/json")
			resp, err := c.http.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			return
		}
	}
	_, _, _, _ = c.doRaw(ctx, http.MethodDelete, path, nil, true)
}

func (c *redfishBackend) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, 0, err
	}
	data, code, _, err := c.doRaw(ctx, method, path, body, true)
	if err != nil {
		return nil, 0, err
	}
	if code == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.sessionID = ""
		c.mu.Unlock()
		if err := c.ensureSession(ctx); err != nil {
			return nil, 0, err
		}
		data, code, _, err = c.doRaw(ctx, method, path, body, true)
	}
	return data, code, err
}

func (c *redfishBackend) doRaw(ctx context.Context, method, path string, body any, withToken bool) ([]byte, int, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withToken {
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		if token != "" {
			req.Header.Set("X-Auth-Token", token)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return data, resp.StatusCode, resp.Header.Clone(), nil
}

func (c *redfishBackend) getJSON(ctx context.Context, path string, dest any) error {
	data, code, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("redfish GET %s: HTTP %d: %s", normalizePath(path), code, truncateErr(data))
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("redfish GET %s: decode: %w", normalizePath(path), err)
	}
	return nil
}

func (c *redfishBackend) postJSON(ctx context.Context, path string, body any) error {
	data, code, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("redfish POST %s: HTTP %d: %s", normalizePath(path), code, truncateErr(data))
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

func (c *redfishBackend) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	var root struct {
		RedfishVersion string `json:"RedfishVersion"`
	}
	var mgr struct {
		FirmwareVersion string `json:"FirmwareVersion"`
		Model           string `json:"Model"`
		ManagerType     string `json:"ManagerType"`
	}
	var sys struct {
		Manufacturer string `json:"Manufacturer"`
		Model        string `json:"Model"`
	}
	info := &bmc.MCInfo{
		Manufacturer:    "Dell",
		Model:           "iDRAC",
		ProtocolVersion: "Redfish",
		FirmwareRev:     "unknown",
	}
	if err := c.getJSON(ctx, "/redfish/v1", &root); err == nil && root.RedfishVersion != "" {
		info.ProtocolVersion = "Redfish " + root.RedfishVersion
	}
	if err := c.getJSON(ctx, "/redfish/v1/Managers/1", &mgr); err == nil {
		if mgr.FirmwareVersion != "" {
			info.FirmwareRev = mgr.FirmwareVersion
		}
		if mgr.Model != "" {
			info.Model = mgr.Model
		} else if mgr.ManagerType != "" {
			info.Model = mgr.ManagerType
		}
	}
	if err := c.getJSON(ctx, "/redfish/v1/Systems/1", &sys); err == nil {
		if sys.Manufacturer != "" {
			info.Manufacturer = sys.Manufacturer
		}
		if sys.Model != "" {
			info.Model = sys.Model
		}
	}
	if info.Model == "" {
		info.Model = "iDRAC"
	}
	return info, nil
}

func (c *redfishBackend) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	var sys struct {
		PowerState string `json:"PowerState"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/Systems/1", &sys); err != nil {
		return nil, err
	}
	return &bmc.PowerStatus{IsOn: strings.EqualFold(sys.PowerState, "On")}, nil
}

func (c *redfishBackend) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	var resetType string
	switch action {
	case bmc.PowerOn:
		resetType = "On"
	case bmc.PowerOff:
		resetType = "ForceOff"
	case bmc.PowerCycle:
		resetType = "ForceRestart"
	case bmc.PowerSoft:
		resetType = "GracefulShutdown"
	default:
		return fmt.Errorf("%w: power action %q", bmc.ErrUnsupported, action)
	}
	return c.postJSON(ctx, "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", map[string]string{"ResetType": resetType})
}

func (c *redfishBackend) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	out, err := c.readThermal(ctx)
	if err != nil || len(out) == 0 {
		out = c.syntheticSensors(ctx)
	}
	return out, nil
}

func (c *redfishBackend) readThermal(ctx context.Context) ([]bmc.Sensor, error) {
	var thermal struct {
		Temperatures []struct {
			Name           string    `json:"Name"`
			MemberID       string    `json:"MemberId"`
			ReadingCelsius float64   `json:"ReadingCelsius"`
			Status         statusObj `json:"Status"`
		} `json:"Temperatures"`
		Fans []struct {
			Name         string    `json:"Name"`
			MemberID     string    `json:"MemberId"`
			Reading      float64   `json:"Reading"`
			ReadingUnits string    `json:"ReadingUnits"`
			Status       statusObj `json:"Status"`
		} `json:"Fans"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/Chassis/1/Thermal", &thermal); err != nil {
		return nil, err
	}
	out := make([]bmc.Sensor, 0, len(thermal.Temperatures)+len(thermal.Fans))
	for i, t := range thermal.Temperatures {
		name := t.Name
		if name == "" {
			name = fmt.Sprintf("Temp-%d", i)
		}
		id := t.MemberID
		if id == "" {
			id = fmt.Sprintf("temp-%d", i)
		}
		out = append(out, bmc.Sensor{
			ID: id, Name: name, Type: "Temperature",
			Value: formatFloat(t.ReadingCelsius), Unit: "C",
			Status: healthStatus(t.Status), Present: true,
		})
	}
	for i, f := range thermal.Fans {
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("Fan-%d", i)
		}
		id := f.MemberID
		if id == "" {
			id = fmt.Sprintf("fan-%d", i)
		}
		unit := f.ReadingUnits
		if unit == "" {
			unit = "RPM"
		}
		out = append(out, bmc.Sensor{
			ID: id, Name: name, Type: "Fan",
			Value: formatFloat(f.Reading), Unit: unit,
			Status: healthStatus(f.Status), Present: true,
		})
	}
	return out, nil
}

func (c *redfishBackend) syntheticSensors(ctx context.Context) []bmc.Sensor {
	out := make([]bmc.Sensor, 0, 2)
	var sys struct {
		Status statusObj `json:"Status"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/Systems/1", &sys); err == nil {
		h := healthStatus(sys.Status)
		out = append(out, bmc.Sensor{
			ID: "system-health", Name: "System Health", Type: "Health",
			Value: h, Status: h, Present: true,
		})
	}
	var power struct {
		PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
		PowerControl       []struct {
			PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
		} `json:"PowerControl"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/Chassis/1/Power", &power); err == nil {
		watts := power.PowerConsumedWatts
		if watts == 0 && len(power.PowerControl) > 0 {
			watts = power.PowerControl[0].PowerConsumedWatts
		}
		out = append(out, bmc.Sensor{
			ID: "power-consumed", Name: "Power Consumed", Type: "Power",
			Value: formatFloat(watts), Unit: "W", Status: "ok", Present: true,
		})
	}
	return out
}

func (c *redfishBackend) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	var coll struct {
		Members []logEntry `json:"Members"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/Systems/1/LogServices/SEL/Entries", &coll); err != nil {
		return nil, err
	}
	entries := make([]bmc.SELEntry, 0, len(coll.Members))
	for _, it := range coll.Members {
		if it.Message == "" && it.ID == "" && it.EntryID == 0 {
			continue
		}
		id := it.ID
		if id == "" && it.EntryID != 0 {
			id = fmt.Sprintf("%d", it.EntryID)
		}
		ts := parseRedfishTime(it.Created)
		if ts.IsZero() {
			ts = parseRedfishTime(it.EventTimestamp)
		}
		sensorType := it.SensorType
		if sensorType == "" {
			sensorType = it.EntryType
		}
		entries = append(entries, bmc.SELEntry{
			ID: id, Timestamp: ts, SensorType: sensorType,
			SensorName: it.Name, Description: it.Message, Severity: it.Severity,
		})
	}
	sortSELDesc(entries)
	return truncateSEL(entries, limit), nil
}

type statusObj struct {
	Health string `json:"Health"`
	State  string `json:"State"`
}

type logEntry struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	Message        string `json:"Message"`
	Severity       string `json:"Severity"`
	EntryType      string `json:"EntryType"`
	SensorType     string `json:"SensorType"`
	Created        string `json:"Created"`
	EventTimestamp string `json:"EventTimestamp"`
	EntryID        int    `json:"EntryId"`
}

func healthStatus(s statusObj) string {
	if s.Health != "" {
		return s.Health
	}
	if s.State != "" {
		return s.State
	}
	return "ok"
}
