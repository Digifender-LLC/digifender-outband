// Package config loads Outband runtime settings from flags and environment.
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

// AMTOptions holds Intel AMT WS-MAN settings.
type AMTOptions struct {
	// TLS selects HTTPS on port 16993 when the host port is 0; otherwise only
	// flips the scheme (explicit Port still wins).
	TLS bool `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// ILOOptions holds HPE iLO Redfish settings.
type ILOOptions struct {
	// InsecureSkipVerify defaults true when nil (iLO self-signed certs are common).
	InsecureSkipVerify *bool `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
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

	// SensorNames maps BMC/SDR sensor names to human-readable UI labels.
	// Keys must match the provider's sensor Name exactly; unmatched sensors keep their SDR name.
	SensorNames map[string]string `json:"sensor_names,omitempty" yaml:"sensor_names,omitempty"`

	IPMI *IPMIOptions `json:"ipmi,omitempty" yaml:"ipmi,omitempty"`
	KVM  *KVMOptions  `json:"kvm,omitempty" yaml:"kvm,omitempty"`
	AMT  *AMTOptions  `json:"amt,omitempty" yaml:"amt,omitempty"`
	ILO  *ILOOptions  `json:"ilo,omitempty" yaml:"ilo,omitempty"`

	// Options holds opaque JSON blobs keyed by provider name for experimental
	// or in-tree backends that do not yet have a typed nest (unlike ipmi/kvm).
	// Prefer typed fields for shipping providers; when both exist, typed wins.
	Options OptionMap `json:"options,omitempty" yaml:"options,omitempty"`
}

// OptionMap is opaque per-provider JSON keyed by provider name.
type OptionMap map[string]json.RawMessage

// UnmarshalYAML accepts nested YAML objects or JSON object/array strings per key.
func (m *OptionMap) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return err
	}
	out := make(OptionMap, len(raw))
	for k, node := range raw {
		var v any
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("options.%s: %w", k, err)
		}
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if len(s) > 0 && (s[0] == '{' || s[0] == '[') {
				if !json.Valid([]byte(s)) {
					return fmt.Errorf("options.%s: invalid JSON string", k)
				}
				out[k] = json.RawMessage(s)
				continue
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("options.%s: %w", k, err)
		}
		out[k] = b
	}
	*m = out
	return nil
}

// SensorDisplayName returns the configured label for an SDR sensor name, or sdr unchanged.
func (h HostConfig) SensorDisplayName(sdr string) string {
	if h.SensorNames != nil {
		if n := strings.TrimSpace(h.SensorNames[sdr]); n != "" {
			return n
		}
	}
	return sdr
}

// CipherID returns the RMCP+ cipher suite ID, or -1 for library default.
func (h HostConfig) CipherID() int {
	if h.IPMI == nil || h.IPMI.CipherSuite == nil {
		return -1
	}
	return *h.IPMI.CipherSuite
}

// AMTTLS reports whether the AMT provider should use HTTPS WS-MAN.
func (h HostConfig) AMTTLS() bool {
	return h.AMT != nil && h.AMT.TLS
}

// ILOInsecureSkipVerify reports whether the iLO Redfish client should skip TLS verify.
// Defaults to true when the ilo nest or the field is omitted (self-signed iLO certs).
func (h HostConfig) ILOInsecureSkipVerify() bool {
	if h.ILO == nil || h.ILO.InsecureSkipVerify == nil {
		return true
	}
	return *h.ILO.InsecureSkipVerify
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

// ProviderOptions returns the opaque options JSON for name, if present.
// Keys are matched case-insensitively after normalizeHosts lowercases them.
func (h HostConfig) ProviderOptions(name string) (json.RawMessage, bool) {
	if len(h.Options) == 0 {
		return nil, false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	raw, ok := h.Options[name]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// OIDCConfig holds optional OpenID Connect settings for the UI gate.
// Enabled when Issuer, ClientID, and RedirectURL are all set. ClientSecret
// may be empty for public clients that rely on PKCE.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Enabled reports whether OIDC SSO is fully configured.
func (o OIDCConfig) Enabled() bool {
	return strings.TrimSpace(o.Issuer) != "" &&
		strings.TrimSpace(o.ClientID) != "" &&
		strings.TrimSpace(o.RedirectURL) != ""
}

func (o OIDCConfig) anySet() bool {
	return strings.TrimSpace(o.Issuer) != "" ||
		strings.TrimSpace(o.ClientID) != "" ||
		strings.TrimSpace(o.ClientSecret) != "" ||
		strings.TrimSpace(o.RedirectURL) != ""
}

// Config holds process configuration. BMC credentials stay server-side.
type Config struct {
	Listen      string
	UIPass      string
	DataDir     string
	DefaultHost string

	Hosts []HostConfig

	OIDC OIDCConfig

	PollSensors   time.Duration
	PollPower     time.Duration
	PollSEL       time.Duration
	PollMCInfo    time.Duration
	RetentionDays int

	// Global KVM defaults applied to legacy single-host inventory.
	KVMPort int
	KVMTLS  bool
}

// Load parses flags (env as defaults) and validates required fields.
//
// Host inventory priority (first match wins):
//  1. OUTBAND_HOSTS — JSON array
//  2. OUTBAND_HOSTS_FILE — path to YAML or JSON file
//  3. Legacy OUTBAND_BMC_* / -bmc-* flags — one synthesized ipmi host
func Load(args []string) (*Config, error) {
	cfg := &Config{
		Listen:        envOr("OUTBAND_LISTEN", ":8080"),
		UIPass:        os.Getenv("OUTBAND_UI_PASS"),
		DataDir:       envOr("OUTBAND_DATA_DIR", "./data"),
		DefaultHost:   os.Getenv("OUTBAND_DEFAULT_HOST"),
		PollSensors:   envDuration("OUTBAND_POLL_SENSORS", 10*time.Second),
		PollPower:     envDuration("OUTBAND_POLL_POWER", 5*time.Second),
		PollSEL:       envDuration("OUTBAND_POLL_SEL", 60*time.Second),
		PollMCInfo:    envDuration("OUTBAND_POLL_MCINFO", 5*time.Minute),
		RetentionDays: envInt("OUTBAND_RETENTION_DAYS", 7),
		KVMPort:       envInt("OUTBAND_KVM_PORT", 7578),
		KVMTLS:        envBool("OUTBAND_KVM_TLS", false),
		OIDC: OIDCConfig{
			Issuer:       os.Getenv("OUTBAND_OIDC_ISSUER"),
			ClientID:     os.Getenv("OUTBAND_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("OUTBAND_OIDC_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("OUTBAND_OIDC_REDIRECT_URL"),
		},
	}

	// Legacy single-host defaults (used only when no inventory is provided).
	legacyHost := envOr("OUTBAND_BMC_HOST", "192.168.9.74")
	legacyPort := envInt("OUTBAND_BMC_PORT", 623)
	legacyUser := envOr("OUTBAND_BMC_USER", "root")
	legacyPass := os.Getenv("OUTBAND_BMC_PASS")
	legacyCipher := envInt("OUTBAND_CIPHER_SUITE", -1)
	hostsFile := os.Getenv("OUTBAND_HOSTS_FILE")

	fs := flag.NewFlagSet("outband", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address")
	fs.StringVar(&cfg.UIPass, "ui-pass", cfg.UIPass, "UI gate password / break-glass (prefer OUTBAND_UI_PASS)")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "SQLite telemetry directory (OUTBAND_DATA_DIR)")
	fs.StringVar(&cfg.DefaultHost, "default-host", cfg.DefaultHost, "Active host id (OUTBAND_DEFAULT_HOST)")
	fs.StringVar(&hostsFile, "hosts-file", hostsFile, "Path to hosts YAML/JSON (OUTBAND_HOSTS_FILE)")
	fs.DurationVar(&cfg.PollSensors, "poll-sensors", cfg.PollSensors, "sensor poll interval")
	fs.DurationVar(&cfg.PollPower, "poll-power", cfg.PollPower, "power poll interval")
	fs.IntVar(&cfg.RetentionDays, "retention-days", cfg.RetentionDays, "telemetry retention days")
	fs.IntVar(&cfg.KVMPort, "kvm-port", cfg.KVMPort, "AMI IVTP video port (OUTBAND_KVM_PORT)")
	fs.BoolVar(&cfg.KVMTLS, "kvm-tls", cfg.KVMTLS, "TLS on IVTP socket (OUTBAND_KVM_TLS)")

	fs.StringVar(&cfg.OIDC.Issuer, "oidc-issuer", cfg.OIDC.Issuer, "OIDC issuer URL (OUTBAND_OIDC_ISSUER)")
	fs.StringVar(&cfg.OIDC.ClientID, "oidc-client-id", cfg.OIDC.ClientID, "OIDC client ID (OUTBAND_OIDC_CLIENT_ID)")
	fs.StringVar(&cfg.OIDC.ClientSecret, "oidc-client-secret", cfg.OIDC.ClientSecret, "OIDC client secret (OUTBAND_OIDC_CLIENT_SECRET)")
	fs.StringVar(&cfg.OIDC.RedirectURL, "oidc-redirect-url", cfg.OIDC.RedirectURL, "OIDC redirect URL (OUTBAND_OIDC_REDIRECT_URL)")

	fs.StringVar(&legacyHost, "bmc-host", legacyHost, "Legacy single BMC hostname or IP")
	fs.IntVar(&legacyPort, "bmc-port", legacyPort, "Legacy BMC IPMI UDP port")
	fs.StringVar(&legacyUser, "bmc-user", legacyUser, "Legacy BMC username")
	fs.StringVar(&legacyPass, "bmc-pass", legacyPass, "Legacy BMC password (prefer OUTBAND_BMC_PASS)")
	fs.IntVar(&legacyCipher, "cipher-suite", legacyCipher, "Legacy RMCP+ cipher suite ID (-1 = default)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	hosts, err := loadHosts(hostsFile, legacyHost, legacyPort, legacyUser, legacyPass, legacyCipher, cfg.KVMPort, cfg.KVMTLS)
	if err != nil {
		return nil, err
	}
	cfg.Hosts = hosts

	if err := validateAuth(cfg); err != nil {
		return nil, err
	}
	if cfg.RetentionDays < 1 {
		cfg.RetentionDays = 7
	}
	if err := validateHosts(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateAuth(cfg *Config) error {
	if cfg.OIDC.anySet() && !cfg.OIDC.Enabled() {
		return fmt.Errorf("OIDC is partially configured: set OUTBAND_OIDC_ISSUER, OUTBAND_OIDC_CLIENT_ID, and OUTBAND_OIDC_REDIRECT_URL together (client secret is optional)")
	}
	if cfg.UIPass == "" && !cfg.OIDC.Enabled() {
		return fmt.Errorf("at least one UI auth method is required: OUTBAND_UI_PASS and/or complete OIDC (OUTBAND_OIDC_ISSUER, OUTBAND_OIDC_CLIENT_ID, OUTBAND_OIDC_REDIRECT_URL)")
	}
	return nil
}

func loadHosts(hostsFile, legacyHost string, legacyPort int, legacyUser, legacyPass string, legacyCipher, kvmPort int, kvmTLS bool) ([]HostConfig, error) {
	if raw := strings.TrimSpace(os.Getenv("OUTBAND_HOSTS")); raw != "" {
		hosts, err := parseHostsJSON([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("OUTBAND_HOSTS: %w", err)
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
		return nil, fmt.Errorf("BMC host is required (OUTBAND_HOSTS, OUTBAND_HOSTS_FILE, or OUTBAND_BMC_HOST)")
	}
	if legacyPass == "" {
		return nil, fmt.Errorf("BMC password is required (OUTBAND_BMC_PASS or host inventory password)")
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
		if h.Port == 0 && h.Provider == "amt" {
			if h.AMT != nil && h.AMT.TLS {
				h.Port = 16993
			} else {
				h.Port = 16992
			}
		}
		if h.Port == 0 && h.Provider == "ilo" {
			h.Port = 443
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

		if len(h.SensorNames) > 0 {
			cp := make(map[string]string, len(h.SensorNames))
			for k, v := range h.SensorNames {
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				if k == "" || v == "" {
					continue
				}
				cp[k] = v
			}
			h.SensorNames = cp
		}

		if len(h.Options) > 0 {
			cp := make(OptionMap, len(h.Options))
			for k, v := range h.Options {
				k = strings.ToLower(strings.TrimSpace(k))
				if k == "" || len(v) == 0 {
					continue
				}
				cp[k] = v
			}
			h.Options = cp
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
