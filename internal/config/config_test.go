package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"outband/internal/config"
)

func TestLoadLegacySingleHost(t *testing.T) {
	t.Setenv("OUTBAND_HOSTS", "")
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	t.Setenv("OUTBAND_BMC_HOST", "10.0.0.1")
	t.Setenv("OUTBAND_BMC_USER", "admin")
	t.Setenv("OUTBAND_BMC_PASS", "secret")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "")
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
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "b")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p1"},
		{"id":"b","name":"Second","provider":"ipmi","host":"2.2.2.2","user":"u","password":"p2",
		 "ipmi":{"cipher_suite":3}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

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
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p",
		 "ipmi":{"cipher_suite":17},"kvm":{"port":8443,"tls":true}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

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
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	// Top-level cipher_suite is unknown on HostConfig; encoding/json ignores it.
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p","cipher_suite":3}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0].CipherID() != -1 {
		t.Fatalf("flat cipher_suite must not set CipherID; got %d", cfg.Hosts[0].CipherID())
	}
}

func TestFlatKVMIgnored(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	// Flat kvm_* are unknown; IPMI still gets default kvm port 7578.
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p","kvm_port":9000,"kvm_tls":true}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

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

func TestAMTDefaultPort(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"amt","host":"192.168.8.45","user":"admin","password":"p"}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	clearOIDCEnv(t)

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if h.Port != 16992 {
		t.Fatalf("port=%d want 16992", h.Port)
	}
	if h.AMTTLS() {
		t.Fatal("tls should be false")
	}
	if h.HasKVM() {
		t.Fatal("amt without kvm must not HasKVM")
	}
}

func TestAMTTLSDefaultPort(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"amt","host":"192.168.8.45","user":"admin","password":"p","amt":{"tls":true}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	clearOIDCEnv(t)

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if h.Port != 16993 {
		t.Fatalf("port=%d want 16993", h.Port)
	}
	if !h.AMTTLS() {
		t.Fatal("tls should be true")
	}
}

func TestILODefaultPort(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ilo","host":"192.168.9.90","user":"Administrator","password":"p"}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	clearOIDCEnv(t)

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if h.Port != 443 {
		t.Fatalf("port=%d want 443", h.Port)
	}
	if !h.ILOInsecureSkipVerify() {
		t.Fatal("insecure_skip_verify should default true")
	}
	if h.HasKVM() {
		t.Fatal("ilo without kvm must not HasKVM")
	}
}

func TestILOInsecureSkipVerifyFalse(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ilo","host":"192.168.9.90","user":"Administrator","password":"p",
		 "ilo":{"insecure_skip_verify":false}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	clearOIDCEnv(t)

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0].ILOInsecureSkipVerify() {
		t.Fatal("insecure_skip_verify should be false")
	}
}

func TestNonIPMIWithoutKVM(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "d")
	// idrac is a registered stub; config load does not open providers.
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"d","provider":"idrac","host":"1.1.1.1","user":"u","password":"p"}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0].HasKVM() {
		t.Fatal("non-ipmi without kvm block must not HasKVM")
	}
}

func TestLoadSensorNames(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p",
		 "sensor_names":{"CPU DTS value":"CPU temperature","Sys.1(CPU)":"CPU fan"}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if got := h.SensorDisplayName("CPU DTS value"); got != "CPU temperature" {
		t.Fatalf("alias=%q", got)
	}
	if got := h.SensorDisplayName("Sys.1(CPU)"); got != "CPU fan" {
		t.Fatalf("alias=%q", got)
	}
	if got := h.SensorDisplayName("Unknown"); got != "Unknown" {
		t.Fatalf("passthrough=%q", got)
	}
}

func TestLoadMissingUser(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"","password":"p"}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

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
		case "ipmi", "idrac", "amt", "ilo":
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
	t.Setenv("OUTBAND_HOSTS", "")
	t.Setenv("OUTBAND_HOSTS_FILE", path)
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "")

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
	t.Setenv("OUTBAND_OIDC_ISSUER", "")
	t.Setenv("OUTBAND_OIDC_CLIENT_ID", "")
	t.Setenv("OUTBAND_OIDC_CLIENT_SECRET", "")
	t.Setenv("OUTBAND_OIDC_REDIRECT_URL", "")
}

func TestAuthRequiresPasswordOrOIDC(t *testing.T) {
	t.Setenv("OUTBAND_HOSTS", `[{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p"}]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
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
	t.Setenv("OUTBAND_HOSTS", `[{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p"}]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_OIDC_ISSUER", "https://idp.example")
	t.Setenv("OUTBAND_OIDC_CLIENT_ID", "")
	t.Setenv("OUTBAND_OIDC_CLIENT_SECRET", "")
	t.Setenv("OUTBAND_OIDC_REDIRECT_URL", "")

	_, err := config.Load(nil)
	if err == nil {
		t.Fatal("expected partial OIDC error")
	}
	if !strings.Contains(err.Error(), "partially configured") {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthOIDCWithoutPassword(t *testing.T) {
	t.Setenv("OUTBAND_HOSTS", `[{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p"}]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_OIDC_ISSUER", "https://idp.example")
	t.Setenv("OUTBAND_OIDC_CLIENT_ID", "outband")
	t.Setenv("OUTBAND_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("OUTBAND_OIDC_REDIRECT_URL", "https://outband.example/auth/oidc/callback")

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
		ClientID:    "c",
		RedirectURL: "https://app/callback",
	}
	if !on.Enabled() {
		t.Fatal("complete config should be enabled")
	}
}

func TestLoadOptionsJSON(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "d")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"d","provider":"idrac","host":"1.1.1.1","user":"u","password":"p",
		 "options":{"DigitalOcean":{"region":"nyc3","droplet_id":123}}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	raw, ok := h.ProviderOptions("digitalocean")
	if !ok {
		t.Fatal("expected ProviderOptions for digitalocean")
	}
	var got struct {
		Region    string `json:"region"`
		DropletID int    `json:"droplet_id"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Region != "nyc3" || got.DropletID != 123 {
		t.Fatalf("got %+v", got)
	}
	if _, ok := h.ProviderOptions("missing"); ok {
		t.Fatal("expected missing options")
	}
	// Existing IPMI loads must still work when options is absent.
	if h.HasKVM() {
		t.Fatal("non-ipmi without kvm must not HasKVM")
	}
}

func TestLoadOptionsYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	content := `
- id: droplet
  provider: idrac
  host: api.example.com
  user: token
  password: secret
  options:
    digitalocean:
      region: nyc3
      droplet_id: 99
    agent: '{"socket":"/run/agent.sock"}'
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OUTBAND_HOSTS", "")
	t.Setenv("OUTBAND_HOSTS_FILE", path)
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "droplet")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	raw, ok := h.ProviderOptions("digitalocean")
	if !ok {
		t.Fatal("expected digitalocean options")
	}
	var do struct {
		Region    string `json:"region"`
		DropletID int    `json:"droplet_id"`
	}
	if err := json.Unmarshal(raw, &do); err != nil {
		t.Fatal(err)
	}
	if do.Region != "nyc3" || do.DropletID != 99 {
		t.Fatalf("digitalocean %+v", do)
	}
	agent, ok := h.ProviderOptions("agent")
	if !ok {
		t.Fatal("expected agent options")
	}
	var ag struct {
		Socket string `json:"socket"`
	}
	if err := json.Unmarshal(agent, &ag); err != nil {
		t.Fatal(err)
	}
	if ag.Socket != "/run/agent.sock" {
		t.Fatalf("agent socket=%q", ag.Socket)
	}
}

func TestIPMIUnchangedWithUnrelatedOptions(t *testing.T) {
	t.Setenv("OUTBAND_BMC_PASS", "")
	t.Setenv("OUTBAND_UI_PASS", "uipass")
	t.Setenv("OUTBAND_DEFAULT_HOST", "a")
	t.Setenv("OUTBAND_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p",
		 "ipmi":{"cipher_suite":3},
		 "options":{"other":{"x":1}}}
	]`)
	t.Setenv("OUTBAND_HOSTS_FILE", "")

	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hosts[0]
	if h.CipherID() != 3 {
		t.Fatalf("cipher=%d", h.CipherID())
	}
	if !h.HasKVM() {
		t.Fatal("ipmi should still default KVM")
	}
	if _, ok := h.ProviderOptions("other"); !ok {
		t.Fatal("expected unrelated options preserved")
	}
}
