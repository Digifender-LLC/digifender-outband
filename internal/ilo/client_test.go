package ilo

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"outband/internal/bmc"
)

func TestFeatures(t *testing.T) {
	a := New(Config{Host: "127.0.0.1", User: "Administrator", Password: "x"})
	f := a.Features()
	if !f.Has(bmc.FeaturePower) || !f.Has(bmc.FeatureIdentity) || !f.Has(bmc.FeatureSensors) || !f.Has(bmc.FeatureSEL) {
		t.Fatalf("features=%v", f)
	}
	if f.Has(bmc.FeatureConsole) || f.Has(bmc.FeatureKVM) {
		t.Fatalf("console/kvm must be omitted: %v", f)
	}
}

func TestDefaultPortAndTrailingSlash(t *testing.T) {
	c := newRedfish(Config{Host: "h", User: "u", Password: "p", InsecureSkipVerify: true})
	if !strings.HasPrefix(c.baseURL, "https://h:443") {
		t.Fatalf("baseURL=%s", c.baseURL)
	}
	if got := ensureTrailingSlash("/redfish/v1/Systems/1"); got != "/redfish/v1/Systems/1/" {
		t.Fatalf("slash=%q", got)
	}
	if got := ensureTrailingSlash("/redfish/v1/Systems/1/"); got != "/redfish/v1/Systems/1/" {
		t.Fatalf("idempotent slash=%q", got)
	}
}

func TestRedfishHTTPtest(t *testing.T) {
	var sawReset atomic.Pointer[string]
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "Administrator" || pass != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="iLO"`)
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Error(w, "missing trailing slash: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/":
			writeJSON(w, map[string]any{
				"RedfishVersion": "1.0.0",
				"Oem": map[string]any{
					"Hp": map[string]any{
						"Manager": []map[string]any{{
							"ManagerType":            "iLO 4",
							"ManagerFirmwareVersion": "2.82",
						}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/Managers/1/":
			writeJSON(w, map[string]any{"FirmwareVersion": "iLO 4 v2.82"})
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/Systems/1/":
			writeJSON(w, map[string]any{
				"Manufacturer": "HPE",
				"Model":        "DL380p Gen8",
				"PowerState":   "On",
				"Status":       map[string]string{"Health": "Warning", "State": "Starting"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/":
			body, _ := io.ReadAll(r.Body)
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			v := m["ResetType"]
			sawReset.Store(&v)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/Chassis/1/Thermal/":
			http.Error(w, "thermal unavailable", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/Chassis/1/Power/":
			writeJSON(w, map[string]any{"PowerConsumedWatts": 120})
		case r.Method == http.MethodGet && r.URL.Path == "/redfish/v1/Managers/1/LogServices/IEL/Entries/":
			writeJSON(w, map[string]any{
				"Items": []map[string]any{
					{"Id": "1", "RecordId": 1, "Message": "Power restored to iLO.", "Severity": "Warning", "OemRecordFormat": "Hp-iLOEventLog", "Name": "iLO Event Log"},
					{"Id": "2", "RecordId": 2, "Message": "Browser login: Administrator.", "Severity": "Informational", "OemRecordFormat": "Hp-iLOEventLog", "Name": "iLO Event Log"},
				},
				"Members": []map[string]any{
					{"@odata.id": "/redfish/v1/Managers/1/LogServices/IEL/Entries/1/"},
					{"@odata.id": "/redfish/v1/Managers/1/LogServices/IEL/Entries/2/"},
				},
			})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, port := splitHostPort(srv.URL)
	a := New(Config{
		Host:               host,
		Port:               port,
		User:               "Administrator",
		Password:           "secret",
		InsecureSkipVerify: true,
	})
	defer a.Close()
	ctx := t.Context()

	info, err := a.MCInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.FirmwareRev != "iLO 4 v2.82" {
		t.Fatalf("firmware=%q", info.FirmwareRev)
	}
	if info.Manufacturer != "HPE" || info.Model != "DL380p Gen8" {
		t.Fatalf("identity=%+v", info)
	}
	if info.ProtocolVersion != "1.0.0" {
		t.Fatalf("protocol=%q", info.ProtocolVersion)
	}

	ps, err := a.PowerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ps.IsOn {
		t.Fatal("expected on")
	}

	if err := a.PowerControl(ctx, bmc.PowerCycle); err != nil {
		t.Fatal(err)
	}
	if p := sawReset.Load(); p == nil || *p != "ForceRestart" {
		t.Fatalf("resetType=%v", p)
	}

	sensors, err := a.Sensors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) < 1 {
		t.Fatal("expected synthetic sensors when thermal fails")
	}
	foundHealth, foundPower := false, false
	for _, s := range sensors {
		if s.Name == "System Health" && s.Value == "Warning" {
			foundHealth = true
		}
		if s.Name == "Power Consumed" && s.Value == "120" {
			foundPower = true
		}
	}
	if !foundHealth || !foundPower {
		t.Fatalf("sensors=%+v", sensors)
	}

	sel, err := a.SEL(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 2 {
		t.Fatalf("sel count=%d", len(sel))
	}
	if sel[0].ID != "2" || !strings.Contains(sel[0].Description, "Browser login") {
		t.Fatalf("newest first: %+v", sel[0])
	}
}

func TestThermalSensors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != "u" {
			http.Error(w, "auth", 401)
			return
		}
		if r.URL.Path == "/redfish/v1/Chassis/1/Thermal/" {
			writeJSON(w, map[string]any{
				"Temperatures": []map[string]any{{
					"Name": "01-Inlet Ambient", "MemberId": "0", "ReadingCelsius": 24,
					"Status": map[string]string{"Health": "OK"},
				}},
				"Fans": []map[string]any{{
					"Name": "Fan 1", "MemberId": "1", "Reading": 3200, "ReadingUnits": "RPM",
					"Status": map[string]string{"Health": "OK"},
				}},
			})
			return
		}
		http.Error(w, "no", 404)
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "u", Password: "p", InsecureSkipVerify: true})
	defer a.Close()

	sensors, err := a.Sensors(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 2 {
		t.Fatalf("sensors=%+v", sensors)
	}
	if sensors[0].Type != "Temperature" || sensors[0].Value != "24" {
		t.Fatalf("temp=%+v", sensors[0])
	}
	if sensors[1].Type != "Fan" || sensors[1].Value != "3200" {
		t.Fatalf("fan=%+v", sensors[1])
	}
}

func TestPowerActionMap(t *testing.T) {
	var got atomic.Pointer[string]
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		v := m["ResetType"]
		got.Store(&v)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "u", Password: "p", InsecureSkipVerify: true})
	defer a.Close()

	cases := []struct {
		action bmc.PowerAction
		want   string
	}{
		{bmc.PowerOn, "On"},
		{bmc.PowerOff, "ForceOff"},
		{bmc.PowerCycle, "ForceRestart"},
		{bmc.PowerSoft, "PushPowerButton"},
	}
	for _, tc := range cases {
		if err := a.PowerControl(t.Context(), tc.action); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if p := got.Load(); p == nil || *p != tc.want {
			t.Fatalf("%s: got %v want %s", tc.action, p, tc.want)
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func splitHostPort(rawURL string) (string, int) {
	// httptest URL is https://127.0.0.1:port
	u := strings.TrimPrefix(rawURL, "https://")
	u = strings.TrimPrefix(u, "http://")
	host, portStr, err := net.SplitHostPort(u)
	if err != nil {
		return "127.0.0.1", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}
