package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mipmi/internal/config"
)

func TestLoadLegacySingleHost(t *testing.T) {
	t.Setenv("MIPMI_HOSTS", "")
	t.Setenv("MIPMI_HOSTS_FILE", "")
	t.Setenv("MIPMI_BMC_HOST", "10.0.0.1")
	t.Setenv("MIPMI_BMC_USER", "admin")
	t.Setenv("MIPMI_BMC_PASS", "secret")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "")
	clearOIDCEnv(t)

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 1 {
		t.Fatalf("hosts=%d", len(cfg.Hosts))
	}
	h := cfg.Hosts[0]
	if h.Provider != "ipmi" || h.Host != "10.0.0.1" || h.Password != "secret" {
		t.Fatalf("unexpected host: %+v", h)
	}
	if cfg.DefaultHost != h.ID {
		t.Fatalf("default=%q want %q", cfg.DefaultHost, h.ID)
	}
	if !h.HasKVM() {
		t.Fatal("legacy ipmi host should have KVM")
	}
	port, _ := h.KVMEndpoint()
	if port != 7578 {
		t.Fatalf("kvm port=%d want 7578", port)
	}
}

func TestLoadHostsJSONEnv(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "b")
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p1"},
		{"id":"b","name":"Second","provider":"ipmi","host":"2.2.2.2","user":"u","password":"p2",
		 "ipmi":{"cipher_suite":3}}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("hosts=%d", len(cfg.Hosts))
	}
	if cfg.DefaultHost != "b" {
		t.Fatalf("default=%q", cfg.DefaultHost)
	}
	if cfg.Hosts[1].CipherID() != 3 {
		t.Fatalf("cipher=%d", cfg.Hosts[1].CipherID())
	}
	if !cfg.Hosts[0].HasKVM() || !cfg.Hosts[1].HasKVM() {
		t.Fatal("ipmi hosts should default-enable KVM")
	}
}

func TestLoadNestedIPMIAndKVM(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "a")
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p",
		 "ipmi":{"cipher_suite":17},"kvm":{"port":8443,"tls":true}}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if h.CipherID() != 17 {
		t.Fatalf("cipher=%d", h.CipherID())
	}
	port, tls := h.KVMEndpoint()
	if port != 8443 || !tls {
		t.Fatalf("kvm endpoint port=%d tls=%v", port, tls)
	}
}

func TestFlatCipherSuiteIgnored(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "a")
	// Top-level cipher_suite is unknown on HostConfig; encoding/json ignores it.
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p","cipher_suite":3}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0].CipherID() != -1 {
		t.Fatalf("flat cipher_suite must not set CipherID; got %d", cfg.Hosts[0].CipherID())
	}
}

func TestFlatKVMIgnored(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "a")
	// Flat kvm_* are unknown; IPMI still gets default kvm port 7578.
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p","kvm_port":9000,"kvm_tls":true}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	port, tls := h.KVMEndpoint()
	if port != 7578 || tls {
		t.Fatalf("flat kvm_* must be ignored; got port=%d tls=%v", port, tls)
	}
}

func TestNonIPMIWithoutKVM(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "d")
	// idrac is a registered stub; config load does not open providers.
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"d","provider":"idrac","host":"1.1.1.1","user":"u","password":"p"}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0].HasKVM() {
		t.Fatal("non-ipmi without kvm block must not HasKVM")
	}
}

func TestLoadMissingUser(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "a")
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"","password":"p"}
	]`)
	t.Setenv("MIPMI_HOSTS_FILE", "")

	_, err := config.Load(nil)
	if err == nil {
		t.Fatal("expected error for missing user")
	}
	if !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateProviders(t *testing.T) {
	known := func(name string) bool {
		switch name {
		case "ipmi", "idrac", "amt":
			return true
		default:
			return false
		}
	}
	cfg := &config.Config{
		Hosts: []config.HostConfig{
			{ID: "a", Provider: "ipmi"},
			{ID: "b", Provider: "idrac"},
		},
	}
	if err := config.ValidateProviders(cfg, known); err != nil {
		t.Fatal(err)
	}
	cfg.Hosts = append(cfg.Hosts, config.HostConfig{ID: "c", Provider: "nope"})
	err := config.ValidateProviders(cfg, known)
	if err == nil {
		t.Fatal("expected unknown provider error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadHostsFileYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	content := `
- id: yaml1
  provider: ipmi
  host: 9.9.9.9
  user: root
  password: pw
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIPMI_HOSTS", "")
	t.Setenv("MIPMI_HOSTS_FILE", path)
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].ID != "yaml1" {
		t.Fatalf("unexpected: %+v", cfg.Hosts)
	}
	if !cfg.Hosts[0].HasKVM() {
		t.Fatal("yaml ipmi should default KVM")
	}
}

func clearOIDCEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MIPMI_OIDC_ISSUER", "")
	t.Setenv("MIPMI_OIDC_CLIENT_ID", "")
	t.Setenv("MIPMI_OIDC_CLIENT_SECRET", "")
	t.Setenv("MIPMI_OIDC_REDIRECT_URL", "")
}

func TestAuthRequiresPasswordOrOIDC(t *testing.T) {
	t.Setenv("MIPMI_HOSTS", "")
	t.Setenv("MIPMI_HOSTS_FILE", "")
	t.Setenv("MIPMI_BMC_HOST", "10.0.0.1")
	t.Setenv("MIPMI_BMC_USER", "admin")
	t.Setenv("MIPMI_BMC_PASS", "secret")
	t.Setenv("MIPMI_UI_PASS", "")
	clearOIDCEnv(t)

	_, err := config.Load(nil)
	if err == nil {
		t.Fatal("expected error when neither password nor OIDC configured")
	}
	if !strings.Contains(err.Error(), "at least one UI auth method") {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthPartialOIDCFails(t *testing.T) {
	t.Setenv("MIPMI_HOSTS", "")
	t.Setenv("MIPMI_HOSTS_FILE", "")
	t.Setenv("MIPMI_BMC_HOST", "10.0.0.1")
	t.Setenv("MIPMI_BMC_USER", "admin")
	t.Setenv("MIPMI_BMC_PASS", "secret")
	t.Setenv("MIPMI_UI_PASS", "")
	t.Setenv("MIPMI_OIDC_ISSUER", "https://idp.example")
	t.Setenv("MIPMI_OIDC_CLIENT_ID", "")
	t.Setenv("MIPMI_OIDC_CLIENT_SECRET", "")
	t.Setenv("MIPMI_OIDC_REDIRECT_URL", "")

	_, err := config.Load(nil)
	if err == nil {
		t.Fatal("expected partial OIDC error")
	}
	if !strings.Contains(err.Error(), "partially configured") {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthOIDCWithoutPassword(t *testing.T) {
	t.Setenv("MIPMI_HOSTS", "")
	t.Setenv("MIPMI_HOSTS_FILE", "")
	t.Setenv("MIPMI_BMC_HOST", "10.0.0.1")
	t.Setenv("MIPMI_BMC_USER", "admin")
	t.Setenv("MIPMI_BMC_PASS", "secret")
	t.Setenv("MIPMI_UI_PASS", "")
	t.Setenv("MIPMI_OIDC_ISSUER", "https://idp.example")
	t.Setenv("MIPMI_OIDC_CLIENT_ID", "mipmi")
	t.Setenv("MIPMI_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("MIPMI_OIDC_REDIRECT_URL", "https://mipmi.example/auth/oidc/callback")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OIDC.Enabled() {
		t.Fatal("OIDC should be enabled")
	}
	if cfg.UIPass != "" {
		t.Fatalf("UIPass=%q", cfg.UIPass)
	}
}

func TestOIDCConfigEnabled(t *testing.T) {
	off := config.OIDCConfig{}
	if off.Enabled() {
		t.Fatal("empty should be disabled")
	}
	on := config.OIDCConfig{
		Issuer:      "https://idp.example",
		ClientID:    "mipmi",
		RedirectURL: "https://mipmi.example/auth/oidc/callback",
	}
	if !on.Enabled() {
		t.Fatal("complete OIDC should be enabled")
	}
}
