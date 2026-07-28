package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

	host := &hosts.Host{
		ID:       "t1",
		Name:     "test",
		Provider: "fake",
		Address:  "127.0.0.1",
		Client:   featureClient{},
		// hasKVM left false (unexported default) → HasKVM() == false
	}
	srv, err := New(host, gate, store, slog.Default(), config.OIDCConfig{})
	if err != nil {
		t.Fatal(err)
	}

	token, exp := gate.issueToken()
	cookie := &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp}
	handler := srv.Handler()

	for _, path := range []string{"/console", "/kvm"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status=%d want 501", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/power", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("/power: got 501, want non-501 (got %d)", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("/power: status=%d want 200", rec.Code)
	}
}
