package config_test

import (
	"os"
	"path/filepath"
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
}

func TestLoadHostsJSONEnv(t *testing.T) {
	t.Setenv("MIPMI_BMC_PASS", "")
	t.Setenv("MIPMI_UI_PASS", "uipass")
	t.Setenv("MIPMI_DEFAULT_HOST", "b")
	t.Setenv("MIPMI_HOSTS", `[
		{"id":"a","provider":"ipmi","host":"1.1.1.1","user":"u","password":"p1"},
		{"id":"b","name":"Second","provider":"ipmi","host":"2.2.2.2","user":"u","password":"p2","cipher_suite":3}
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
}
