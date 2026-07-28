package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/hosts"
	"outband/internal/telemetry"
)

// featureClient advertises power/sensors/SEL/identity but not console or KVM.
type featureClient struct{}

func (featureClient) Features() bmc.FeatureSet {
	return bmc.FeatureSet(bmc.FeaturePower | bmc.FeatureSensors | bmc.FeatureSEL | bmc.FeatureIdentity)
}

func (featureClient) MCInfo(context.Context) (*bmc.MCInfo, error)           { return &bmc.MCInfo{}, nil }
func (featureClient) PowerStatus(context.Context) (*bmc.PowerStatus, error) { return &bmc.PowerStatus{}, nil }
func (featureClient) PowerControl(context.Context, bmc.PowerAction) error   { return nil }
func (featureClient) Sensors(context.Context) ([]bmc.Sensor, error)         { return nil, nil }
func (featureClient) SEL(context.Context, int) ([]bmc.SELEntry, error)      { return nil, nil }
func (featureClient) Close() error                                          { return nil }

func TestFeatureGates(t *testing.T) {
	gate, err := NewGate("testpass")
	if err != nil {
		t.Fatal(err)
	}
	store, err := telemetry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	reg := testRegistry(t, &hosts.Host{
		ID:       "t1",
		Name:     "test",
		Provider: "fake",
		Address:  "127.0.0.1",
		Client:   featureClient{},
		// hasAMIKVM/hasAMTKVM left false → HasKVM() == false
	})
	srv, err := New(reg, gate, store, slog.Default(), config.OIDCConfig{})
	if err != nil {
		t.Fatal(err)
	}

	token, exp := gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}
	handler := srv.Handler()

	for _, path := range []string{"/h/t1/console", "/h/t1/kvm"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status=%d want 501", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/h/t1/power", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("/h/t1/power: got 501, want non-501 (got %d)", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("/h/t1/power: status=%d want 200", rec.Code)
	}
}

func TestRootRedirectsToDefaultHost(t *testing.T) {
	srv := testServer(t, "testpass")
	token, exp := srv.gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/h/t1/" {
		t.Fatalf("Location=%q want /h/t1/", loc)
	}
}

func TestLegacyPathRedirects(t *testing.T) {
	srv := testServer(t, "testpass")
	token, exp := srv.gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}

	req := httptest.NewRequest(http.MethodGet, "/power", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/h/t1/power" {
		t.Fatalf("Location=%q want /h/t1/power", loc)
	}
}

func TestUnknownHostNotFound(t *testing.T) {
	srv := testServer(t, "testpass")
	token, exp := srv.gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}

	req := httptest.NewRequest(http.MethodGet, "/h/missing/", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestHostPickerRendered(t *testing.T) {
	gate, err := NewGate("testpass")
	if err != nil {
		t.Fatal(err)
	}
	store, err := telemetry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	reg := testRegistry(t,
		&hosts.Host{ID: "a", Name: "Host A", Provider: "fake", Address: "10.0.0.1", Client: featureClient{}},
		&hosts.Host{ID: "b", Name: "Host B", Provider: "fake", Address: "10.0.0.2", Client: featureClient{}},
	)
	srv, err := New(reg, gate, store, slog.Default(), config.OIDCConfig{})
	if err != nil {
		t.Fatal(err)
	}

	token, exp := gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}
	req := httptest.NewRequest(http.MethodGet, "/h/a/", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	s := string(body)
	if !strings.Contains(s, `data-host-select`) {
		t.Fatal("expected host picker")
	}
	if !strings.Contains(s, `value="a"`) || !strings.Contains(s, `value="b"`) {
		t.Fatalf("expected both host options in %s", s)
	}
	if !strings.Contains(s, `href="/h/a/power"`) {
		t.Fatal("expected HostBase nav link")
	}
}

func TestInventoryDisablesSensors(t *testing.T) {
	gate, err := NewGate("testpass")
	if err != nil {
		t.Fatal(err)
	}
	store, err := telemetry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	off := false
	h := &hosts.Host{
		ID:       "t1",
		Name:     "test",
		Provider: "fake",
		Address:  "127.0.0.1",
		Client:   featureClient{},
	}
	h.SetFeatureFlags(&config.FeatureFlags{Sensors: &off})
	reg := testRegistry(t, h)
	srv, err := New(reg, gate, store, slog.Default(), config.OIDCConfig{})
	if err != nil {
		t.Fatal(err)
	}
	token, exp := gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/h/t1/sensors", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("/sensors status=%d want 501", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/h/t1/metrics", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("/metrics status=%d want 501", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/h/t1/", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	s := string(body)
	if strings.Contains(s, `href="/h/t1/sensors"`) || strings.Contains(s, `href="/h/t1/metrics"`) {
		t.Fatalf("sensors/metrics nav should be hidden: %s", s)
	}
	if !strings.Contains(s, `href="/h/t1/power"`) {
		t.Fatal("power nav should remain")
	}
}
