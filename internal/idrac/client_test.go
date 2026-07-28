package idrac

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
	a := New(Config{Host: "127.0.0.1", User: "root", Password: "x"})
	f := a.Features()
	if !f.Has(bmc.FeaturePower) || !f.Has(bmc.FeatureIdentity) || !f.Has(bmc.FeatureSensors) || !f.Has(bmc.FeatureSEL) {
		t.Fatalf("features=%v", f)
	}
	if f.Has(bmc.FeatureConsole) || f.Has(bmc.FeatureKVM) {
		t.Fatalf("console/kvm must be omitted: %v", f)
	}
}

func TestFeaturesWebOmitsSEL(t *testing.T) {
	a := New(Config{Host: "127.0.0.1", User: "root", Password: "x", Transport: TransportWeb})
	f := a.Features()
	if !f.Has(bmc.FeaturePower) || !f.Has(bmc.FeatureSensors) || !f.Has(bmc.FeatureIdentity) {
		t.Fatalf("features=%v", f)
	}
	if f.Has(bmc.FeatureSEL) {
		t.Fatal("web transport must omit FeatureSEL")
	}
}

func TestNormalizeTransport(t *testing.T) {
	if normalizeTransport("") != TransportAuto {
		t.Fatal("empty → auto")
	}
	if normalizeTransport("Redfish") != TransportRedfish {
		t.Fatal("case")
	}
	if normalizeTransport("nope") != TransportAuto {
		t.Fatal("unknown → auto")
	}
}

func TestAutoPrefersRedfishOverWeb(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case r.Method == http.MethodGet && (path == "" || path == "/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && path == "/redfish/v1":
			writeJSON(w, map[string]any{"RedfishVersion": "1.8.0", "@odata.id": "/redfish/v1"})
		case r.Method == http.MethodGet && path == "/login.html":
			w.Write([]byte(`<html>iDRAC7 data/login</html>`))
		case r.Method == http.MethodPost && path == "/redfish/v1/SessionService/Sessions":
			w.Header().Set("X-Auth-Token", "tok")
			w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/1")
			w.WriteHeader(http.StatusCreated)
		case r.Header.Get("X-Auth-Token") == "tok" && path == "/redfish/v1/Systems/1":
			writeJSON(w, map[string]any{"PowerState": "On", "Manufacturer": "Dell Inc.", "Model": "R740"})
		case r.Header.Get("X-Auth-Token") == "tok" && path == "/redfish/v1/Managers/1":
			writeJSON(w, map[string]any{"FirmwareVersion": "5.00.00.00", "Model": "iDRAC 9"})
		case r.Header.Get("X-Auth-Token") == "tok" && path == "/redfish/v1":
			writeJSON(w, map[string]any{"RedfishVersion": "1.8.0"})
		default:
			http.Error(w, "no "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "u", Password: "p", InsecureSkipVerify: true, Transport: TransportAuto})
	defer a.Close()
	if _, err := a.PowerStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.TransportName() != TransportRedfish {
		t.Fatalf("transport=%s want redfish", a.TransportName())
	}
}

func TestWebBackendIDRAC7(t *testing.T) {
	var sawSet atomic.Pointer[string]
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case r.Method == http.MethodGet && (path == "" || path == "/"):
			w.WriteHeader(200)
		case r.Method == http.MethodGet && path == "/redfish/v1":
			http.Error(w, "no redfish", 404)
		case r.Method == http.MethodGet && path == "/login.html":
			w.Header().Set("Set-Cookie", "_appwebSessionId_=abc; path=/; secure")
			w.Write([]byte(`<html>Integrated Dell Remote Access Controller 7 / iDRAC7<form name="auth"></form>data/login</html>`))
		case r.Method == http.MethodPost && path == "/data/login":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "user=root") || !strings.Contains(string(body), "password=calvin") {
				http.Error(w, "bad creds", 401)
				return
			}
			w.Write([]byte(`<?xml version="1.0"?><authResult>0</authResult><forwardUrl>index.html?ST2=deadbeef</forwardUrl>`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.RawQuery, "get="):
			if r.Header.Get("ST2") != "deadbeef" {
				t.Errorf("missing ST2 header")
			}
			q := r.URL.Query().Get("get")
			var b strings.Builder
			b.WriteString(`<status>ok</status>`)
			for _, k := range strings.Split(q, ",") {
				switch k {
				case "pwState":
					b.WriteString(`<pwState>1</pwState>`)
				case "sysDesc":
					b.WriteString(`<sysDesc>PowerEdge R620</sysDesc>`)
				case "fwVersion":
					b.WriteString(`<fwVersion>1.57.57</fwVersion>`)
				case "svcTag":
					b.WriteString(`<svcTag>3QFPZV1</svcTag>`)
				}
			}
			w.Write([]byte(b.String()))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.RawQuery, "set=pwState:"):
			v := strings.TrimPrefix(r.URL.RawQuery, "set=pwState:")
			sawSet.Store(&v)
			w.Write([]byte(`<status>ok</status>`))
		case r.Method == http.MethodGet && path == "/data/logout":
			w.WriteHeader(200)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery, 404)
		}
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "root", Password: "calvin", InsecureSkipVerify: true, Transport: TransportAuto})
	defer a.Close()

	info, err := a.MCInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "PowerEdge R620" || info.FirmwareRev != "1.57.57" {
		t.Fatalf("info=%+v", info)
	}
	if a.TransportName() != TransportWeb {
		t.Fatalf("transport=%s want web", a.TransportName())
	}
	if a.Features().Has(bmc.FeatureSEL) {
		t.Fatal("resolved web transport must omit FeatureSEL")
	}
	ps, err := a.PowerStatus(t.Context())
	if err != nil || !ps.IsOn {
		t.Fatalf("power=%v err=%v", ps, err)
	}
	if err := a.PowerControl(t.Context(), bmc.PowerCycle); err != nil {
		t.Fatal(err)
	}
	if p := sawSet.Load(); p == nil || *p != "2" {
		t.Fatalf("pwState set=%v", p)
	}
}

func TestRedfishSessionForced(t *testing.T) {
	var sawReset atomic.Pointer[string]
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if r.Method == http.MethodGet && (path == "" || path == "/") {
			w.WriteHeader(200)
			return
		}
		if r.Method == http.MethodGet && path == "/redfish/v1" {
			writeJSON(w, map[string]any{"RedfishVersion": "1.8.0", "@odata.id": "/redfish/v1"})
			return
		}
		if r.Method == http.MethodPost && path == "/redfish/v1/SessionService/Sessions" {
			w.Header().Set("X-Auth-Token", "tok-test")
			w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/1")
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{"Id": "1"})
			return
		}
		if r.Header.Get("X-Auth-Token") != "tok-test" {
			http.Error(w, "need session", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && path == "/redfish/v1":
			writeJSON(w, map[string]any{"RedfishVersion": "1.8.0"})
		case r.Method == http.MethodGet && path == "/redfish/v1/Managers/1":
			writeJSON(w, map[string]any{"FirmwareVersion": "5.00.00.00", "Model": "iDRAC 9"})
		case r.Method == http.MethodGet && path == "/redfish/v1/Systems/1":
			writeJSON(w, map[string]any{
				"Manufacturer": "Dell Inc.", "Model": "PowerEdge R740",
				"PowerState": "On", "Status": map[string]string{"Health": "Warning"},
			})
		case r.Method == http.MethodPost && path == "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset":
			body, _ := io.ReadAll(r.Body)
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			v := m["ResetType"]
			sawReset.Store(&v)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && path == "/redfish/v1/Chassis/1/Thermal":
			http.Error(w, "thermal unavailable", 500)
		case r.Method == http.MethodGet && path == "/redfish/v1/Chassis/1/Power":
			writeJSON(w, map[string]any{"PowerConsumedWatts": 210})
		case r.Method == http.MethodGet && path == "/redfish/v1/Systems/1/LogServices/SEL/Entries":
			writeJSON(w, map[string]any{
				"Members": []map[string]any{
					{"Id": "1", "EntryId": 1, "Message": "on", "Severity": "OK"},
					{"Id": "2", "EntryId": 2, "Message": "cleared", "Severity": "OK"},
				},
			})
		case r.Method == http.MethodDelete && path == "/redfish/v1/SessionService/Sessions/1":
			w.WriteHeader(200)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{
		Host: host, Port: port, User: "root", Password: "calvin",
		InsecureSkipVerify: true, Transport: TransportRedfish,
	})
	defer a.Close()
	ctx := t.Context()

	info, err := a.MCInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "PowerEdge R740" {
		t.Fatalf("model=%q", info.Model)
	}
	if err := a.PowerControl(ctx, bmc.PowerSoft); err != nil {
		t.Fatal(err)
	}
	if p := sawReset.Load(); p == nil || *p != "GracefulShutdown" {
		t.Fatalf("reset=%v", p)
	}
	sensors, err := a.Sensors(ctx)
	if err != nil || len(sensors) < 1 {
		t.Fatalf("sensors=%v err=%v", sensors, err)
	}
	sel, err := a.SEL(ctx, 10)
	if err != nil || len(sel) != 2 || sel[0].ID != "2" {
		t.Fatalf("sel=%+v err=%v", sel, err)
	}
}

func TestPowerActionMapWeb(t *testing.T) {
	var got atomic.Pointer[string]
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case path == "" || path == "/":
			w.WriteHeader(200)
		case path == "/login.html":
			w.Write([]byte(`iDRAC7 data/login`))
		case path == "/data/login":
			w.Write([]byte(`<authResult>0</authResult><forwardUrl>index.html?ST2=abc</forwardUrl>`))
		case strings.HasPrefix(r.URL.RawQuery, "set=pwState:"):
			v := strings.TrimPrefix(r.URL.RawQuery, "set=pwState:")
			got.Store(&v)
			w.Write([]byte(`<status>ok</status>`))
		default:
			http.Error(w, "x", 404)
		}
	}))
	defer srv.Close()
	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "u", Password: "p", InsecureSkipVerify: true, Transport: TransportWeb})
	defer a.Close()
	cases := []struct {
		action bmc.PowerAction
		want   string
	}{
		{bmc.PowerOn, "1"},
		{bmc.PowerOff, "0"},
		{bmc.PowerCycle, "2"},
		{bmc.PowerSoft, "5"},
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
	u := strings.TrimPrefix(rawURL, "https://")
	u = strings.TrimPrefix(u, "http://")
	host, portStr, err := net.SplitHostPort(u)
	if err != nil {
		return "127.0.0.1", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}
