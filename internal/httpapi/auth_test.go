package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mipmi/internal/bmc"
	"mipmi/internal/config"
	"mipmi/internal/hosts"
	"mipmi/internal/telemetry"
)

func testServer(t *testing.T, pass string) *Server {
	t.Helper()
	gate, err := NewGate(pass)
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
	}
	srv, err := New(host, gate, store, slog.Default(), config.OIDCConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestAuthExemptOIDCPaths(t *testing.T) {
	srv := testServer(t, "testpass")
	handler := srv.Handler()

	// Unauthenticated /auth/oidc/login must not redirect to /login (OIDC unset → 404).
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("got redirect to %s, want not redirected by auth middleware", rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 (OIDC not configured)", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusSeeOther && strings.Contains(rec.Header().Get("Location"), "/login") {
		t.Fatal("callback redirected to login by middleware")
	}
}

func TestLoginPagePasswordOnly(t *testing.T) {
	srv := testServer(t, "testpass")
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	s := string(body)
	if !strings.Contains(s, `name="password"`) {
		t.Fatal("expected password form")
	}
	if strings.Contains(s, "/auth/oidc/login") {
		t.Fatal("did not expect SSO link without OIDC")
	}
}

func TestOIDCCallbackStateMismatch(t *testing.T) {
	srv := testServer(t, "testpass")
	// Force a non-nil oidc stub so callback runs validation (no live IdP).
	srv.oidc = &oidcAuth{}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=x&state=wrong", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "expected"})
	req.AddCookie(&http.Cookie{Name: oidcNonceCookie, Value: "nonce"})
	req.AddCookie(&http.Cookie{Name: oidcPKCECookie, Value: "pkce"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "SSO login failed") {
		t.Fatalf("body=%s", body)
	}
}

func TestOIDCCallbackMissingCookies(t *testing.T) {
	srv := testServer(t, "testpass")
	srv.oidc = &oidcAuth{}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=x&state=s", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

// Ensure featureClient still satisfies bmc.Client for this file's helpers.
var _ bmc.Client = featureClient{}
