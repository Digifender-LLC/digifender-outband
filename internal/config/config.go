// Package config loads mIPMI runtime settings from flags and environment.
package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// IPMIOptions holds IPMI/RMCP+-specific settings.
type IPMIOptions struct {
	CipherSuite *int `json:"cipher_suite,omitempty" yaml:"cipher_suite,omitempty"`
}

// KVMOptions holds AMI Adviser/IVTP KVM settings. Presence of this block enables KVM.
type KVMOptions struct {
	Port int  `json:"port,omitempty" yaml:"port,omitempty"` // 0 → default 7578 when block present
	TLS  bool `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// HostConfig describes one BMC/host entry in the inventory.
type HostConfig struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name"`
	Provider string `json:"provider" yaml:"provider"`
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	User     string `json:"user" yaml:"user"`
	Password string `json:"password" yaml:"password"`

	IPMI *IPMIOptions `json:"ipmi,omitempty" yaml:"ipmi,omitempty"`
	KVM  *KVMOptions  `json:"kvm,omitempty" yaml:"kvm,omitempty"`
}

// CipherID returns the RMCP+ cipher suite ID, or -1 for library default.
func (h HostConfig) CipherID() int {
	if h.IPMI == nil || h.IPMI.CipherSuite == nil {
		return -1
	}
	return *h.IPMI.CipherSuite
}

// HasKVM reports whether AMI KVM is configured for this host.
func (h HostConfig) HasKVM() bool {
	return h.KVM != nil
}

// KVMEndpoint returns the IVTP port and TLS flag. Port 0 becomes 7578.
// Only meaningful when HasKVM() is true.
func (h HostConfig) KVMEndpoint() (port int, tls bool) {
	if h.KVM == nil {
		return 0, false
	}
	port = h.KVM.Port
	if port == 0 {
		port = 7578
	}
	return port, h.KVM.TLS
}

// Config holds process configuration. BMC credentials stay server-side.
type Config struct {
	Listen      string
	UIPass      string
	DataDir     string
	DefaultHost string

	Hosts []HostConfig

	PollSensors   time.Duration
	PollPower     time.Duration
	PollSEL       time.Duration
	PollMCInfo    time.Duration
	RetentionDays int

	// Global KVM defaults applied to legacy single-host inventory.
	KVMPort int
	KVMTLS bool
}

// Load parses flags (env as defaults) and validates required fields.
//
// Host inventory priority (first match wins):
//  1. MIPMI_HOSTS — JSON array
//  2. MIPMI_HOSTS_FILE — path to YAML or JSON file
//  3. Legacy MIPMI_BMC_* / -bmc-* flags — one synthesized ipmi host
func Load(args []string) (*Config, error) {
	cfg := &Config{
		Listen:        envOr("MIPMI_LISTEN", ":8080"),
		UIPass:        os.Getenv("MIPMI_UI_PASS"),
		DataDir:       envOr("MIPMI_DATA_DIR", "./data"),
		DefaultHost:   os.Getenv("MIPMI_DEFAULT_HOST"),
		PollSensors:   envDuration("MIPMI_POLL_SENSORS", 10*time.Second),
		PollPower:     envDuration("MIPMI_POLL_POWER", 5*time.Second),
		PollSEL:       envDuration("MIPMI_POLL_SEL", 60*time.Second),
		PollMCInfo:    envDuration("MIPMI_POLL_MCINFO", 5*time.Minute),
		RetentionDays: envInt("MIPMI_RETENTION_DAYS", 7),
		KVMPort:       envInt("MIPMI_KVM_PORT", 7578),
		KVMTLS:       envBool("MIPMI_KVM_TLS", false),
	}

	// Legacy single-host defaults (used only when no inventory is provided).
	legacyHost := envOr("MIPMI_BMC_HOST", "192.168.9.74")
	legacyPort := envInt("MIPMI_BMC_PORT", 623)
	legacyUser := envOr("MIPMI_BMC_USER", "root")
	legacyPass := os.Getenv("MIPMI_BMC_PASS")
	legacyCipher := envInt("MIPMI_CIPHER_SUITE", -1)
	hostsFile := os.Getenv("MIPMI_HOSTS_FILE")

	fs := flag.NewFlagSet("mipmi", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address")
	fs.StringVar(&cfg.UIPass, "ui-pass", cfg.UIPass, "UI gate password (prefer MIPMI_UI_PASS)")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "SQLite telemetry directory (MIPMI_DATA_DIR)")
	fs.StringVar(&cfg.DefaultHost, "default-host", cfg.DefaultHost, "Active host id (MIPMI_DEFAULT_HOST)")
	fs.StringVar(&hostsFile, "hosts-file", hostsFile, "Path to hosts YAML/JSON (MIPMI_HOSTS_FILE)")
	fs.DurationVar(&cfg.PollSensors, "poll-sensors", cfg.PollSensors, "sensor poll interval")
	fs.DurationVar(&cfg.PollPower, "poll-power", cfg.PollPower, "power poll interval")
	fs.IntVar(&cfg.RetentionDays, "retention-days", cfg.RetentionDays, "telemetry retention days")
	fs.IntVar(&cfg.KVMPort, "kvm-port", cfg.KVMPort, "AMI IVTP video port (MIPMI_KVM_PORT)")
	fs.BoolVar(&cfg.KVMTLS, "kvm-tls", cfg.KVMTLS, "TLS on IVTP socket (MIPMI_KVM_TLS)")

	fs.StringVar(&legacyHost, "bmc-host", legacyHost, "Legacy single BMC hostname or IP")
	fs.IntVar(&legacyPort, "bmc-port", legacyPort, "Legacy BMC IPMI UDP port")
	fs.StringVar(&legacyUser, "bmc-user", legacyUser, "Legacy BMC username")
	fs.StringVar(&legacyPass, "bmc-pass", legacyPass, "Legacy BMC password (prefer MIPMI_BMC_PASS)")
	fs.IntVar(&legacyCipher, "cipher-suite", legacyCipher, "Legacy RMCP+ cipher suite ID (-1 = default)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	hosts, err := loadHosts(hostsFile, legacyHost, legacyPort, legacyUser, legacyPass, legacyCipher, cfg.KVMPort, cfg.KVMTLS)
	if err != nil {
		return nil, err
	}
	cfg.Hosts = hosts

	if cfg.UIPass == "" {
		return nil, fmt.Errorf("UI password is required (MIPMI_UI_PASS)")
	}
	if cfg.RetentionDays < 1 {
		cfg.RetentionDays = 7
	}
	if err := validateHosts(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadHosts(hostsFile, legacyHost string, legacyPort int, legacyUser, legacyPass string, legacyCipher, kvmPort int, kvmTLS bool) ([]HostConfig, error) {
	if raw := strings.TrimSpace(os.Getenv("MIPMI_HOSTS")); raw != "" {
		hosts, err := parseHostsJSON([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("MIPMI_HOSTS: %w", err)
		}
		return hosts, nil
	}
	if strings.TrimSpace(hostsFile) != "" {
		hosts, err := loadHostsFile(hostsFile)
		if err != nil {
			return nil, fmt.Errorf("hosts file %s: %w", hostsFile, err)
		}
		return hosts, nil
	}

	// Legacy single-host path.
	if strings.TrimSpace(legacyHost) == "" {
		return nil, fmt.Errorf("BMC host is required (MIPMI_HOSTS, MIPMI_HOSTS_FILE, or MIPMI_BMC_HOST)")
	}
	if legacyPass == "" {
		return nil, fmt.Errorf("BMC password is required (MIPMI_BMC_PASS or host inventory password)")
	}
	id := sanitizeID(legacyHost)
	cipher := legacyCipher
	return normalizeHosts([]HostConfig{{
		ID:       id,
		Name:     legacyHost,
		Provider: "ipmi",
		Host:     legacyHost,
		Port:     legacyPort,
		User:     legacyUser,
		Password: legacyPass,
		IPMI:     &IPMIOptions{CipherSuite: &cipher},
		KVM:      &KVMOptions{Port: kvmPort, TLS: kvmTLS},
	}})
}

func loadHostsFile(path string) ([]HostConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Prefer JSON when the file looks like JSON; otherwise YAML (also accepts JSON).
	trim := strings.TrimSpace(string(b))
	if strings.HasPrefix(trim, "[") || strings.HasPrefix(trim, "{") {
		if hosts, err := parseHostsJSON(b); err == nil {
			return hosts, nil
		}
	}
	return parseHostsYAML(b)
}

func parseHostsJSON(b []byte) ([]HostConfig, error) {
	var hosts []HostConfig
	if err := json.Unmarshal(b, &hosts); err != nil {
		// Allow {"hosts":[...]} wrapper.
		var wrap struct {
			Hosts []HostConfig `json:"hosts"`
		}
		if err2 := json.Unmarshal(b, &wrap); err2 != nil || len(wrap.Hosts) == 0 {
			return nil, err
		}
		hosts = wrap.Hosts
	}
	return normalizeHosts(hosts)
}

func parseHostsYAML(b []byte) ([]HostConfig, error) {
	var hosts []HostConfig
	if err := yaml.Unmarshal(b, &hosts); err != nil {
		var wrap struct {
			Hosts []HostConfig `yaml:"hosts"`
		}
		if err2 := yaml.Unmarshal(b, &wrap); err2 != nil || len(wrap.Hosts) == 0 {
			return nil, err
		}
		hosts = wrap.Hosts
	}
	return normalizeHosts(hosts)
}

func normalizeHosts(hosts []HostConfig) ([]HostConfig, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("host inventory is empty")
	}
	out := make([]HostConfig, len(hosts))
	for i, h := range hosts {
		h.ID = strings.TrimSpace(h.ID)
		h.Name = strings.TrimSpace(h.Name)
		h.Provider = strings.ToLower(strings.TrimSpace(h.Provider))
		h.Host = strings.TrimSpace(h.Host)
		h.User = strings.TrimSpace(h.User)
		if h.Provider == "" {
			h.Provider = "ipmi"
		}
		if h.Port == 0 && h.Provider == "ipmi" {
			h.Port = 623
		}
		if h.ID == "" {
			h.ID = sanitizeID(h.Host)
		}
		if h.Name == "" {
			h.Name = h.Host
		}

		// Preserve AMI-via-IPMI default: enable KVM unless explicitly omitted for non-ipmi.
		if h.Provider == "ipmi" && h.KVM == nil {
			h.KVM = &KVMOptions{Port: 7578}
		}

		out[i] = h
	}
	return out, nil
}

func validateHosts(cfg *Config) error {
	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("at least one host is required")
	}
	seen := make(map[string]struct{}, len(cfg.Hosts))
	for i, h := range cfg.Hosts {
		if h.ID == "" {
			return fmt.Errorf("hosts[%d]: id is required", i)
		}
		if _, ok := seen[h.ID]; ok {
			return fmt.Errorf("duplicate host id %q", h.ID)
		}
		seen[h.ID] = struct{}{}
		if h.Host == "" {
			return fmt.Errorf("host %q: address (host) is required", h.ID)
		}
		if h.User == "" {
			return fmt.Errorf("host %q: user is required", h.ID)
		}
		if h.Password == "" {
			return fmt.Errorf("host %q: password is required", h.ID)
		}
		if h.Provider == "" {
			return fmt.Errorf("host %q: provider is required", h.ID)
		}
	}
	if cfg.DefaultHost == "" {
		cfg.DefaultHost = cfg.Hosts[0].ID
	}
	if _, ok := seen[cfg.DefaultHost]; !ok {
		return fmt.Errorf("default host %q not found in inventory", cfg.DefaultHost)
	}
	return nil
}

// ValidateProviders checks that every host's provider name is known.
// known is typically provider.Known; passed in to avoid an import cycle.
func ValidateProviders(cfg *Config, known func(string) bool) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if known == nil {
		return fmt.Errorf("known provider check is nil")
	}
	for _, h := range cfg.Hosts {
		if !known(h.Provider) {
			return fmt.Errorf("host %q: unknown provider %q", h.ID, h.Provider)
		}
	}
	return nil
}

func sanitizeID(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '.' || r == ':':
			b.WriteByte('-')
		}
	}
	id := b.String()
	if id == "" {
		return "default"
	}
	return id
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
