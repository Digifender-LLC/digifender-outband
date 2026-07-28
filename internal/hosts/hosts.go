// Package hosts manages the in-process BMC host registry.
package hosts

import (
	"errors"
	"fmt"
	"log/slog"

	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/provider"
)

// Host is a registered BMC with an open client.
type Host struct {
	ID          string
	Name        string
	Provider    string
	Address     string
	Port        int // management port (WS-MAN / IPMI / Redfish)
	User        string
	Password    string // server-side only; used for AMI web / KVM
	hasAMIKVM   bool
	hasAMTKVM   bool
	hasILOKVM   bool
	kvmPort     int
	kvmTLS      bool
	iloInsecure bool // iLO TLS skip-verify for IRC + Redfish
	amtTLS      bool // AMT WS-MAN HTTPS
	// sensorNames maps SDR names → display labels (from inventory sensor_names).
	sensorNames map[string]string
	// featureFlags optionally masks provider capabilities (from inventory features).
	featureFlags *config.FeatureFlags
	Client       bmc.Client
}

// DisplayName returns a UI label (name if set, else address).
func (h *Host) DisplayName() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Address
}

// RenameSensor returns the configured display name for an SDR sensor, or name unchanged.
func (h *Host) RenameSensor(name string) string {
	if h == nil || h.sensorNames == nil {
		return name
	}
	if n := h.sensorNames[name]; n != "" {
		return n
	}
	return name
}

// HasKVM reports whether any KVM backend is configured for this host.
func (h *Host) HasKVM() bool {
	return h != nil && (h.hasAMIKVM || h.hasAMTKVM || h.hasILOKVM)
}

// HasAMIKVM reports whether AMI Adviser/IVTP KVM is configured.
func (h *Host) HasAMIKVM() bool {
	return h != nil && h.hasAMIKVM
}

// HasAMTKVM reports whether AMT Hardware-KVM is configured.
func (h *Host) HasAMTKVM() bool {
	return h != nil && h.hasAMTKVM
}

// HasILOKVM reports whether iLO IRC remote console is enabled.
func (h *Host) HasILOKVM() bool {
	return h != nil && h.hasILOKVM
}

// ILOInsecureSkipVerify reports whether iLO TLS verify should be skipped.
func (h *Host) ILOInsecureSkipVerify() bool {
	return h != nil && h.iloInsecure
}

// AMTTLS reports whether AMT WS-MAN uses HTTPS.
func (h *Host) AMTTLS() bool {
	return h != nil && h.amtTLS
}

// KVMPort returns the video/redirection port (meaningful when HasKVM).
func (h *Host) KVMPort() int {
	if h == nil {
		return 0
	}
	return h.kvmPort
}

// KVMTLS reports whether the KVM socket uses TLS (meaningful when HasKVM).
func (h *Host) KVMTLS() bool {
	return h != nil && h.kvmTLS
}

// Features returns the effective capability set for UI nav, route gates, and telemetry.
// Starts from the BMC client, overlays inventory KVM, then applies features.* disables.
func (h *Host) Features() bmc.FeatureSet {
	if h == nil {
		return 0
	}
	features := bmc.ClientFeatures(h.Client)
	// FeatureKVM is owned by host inventory KVM config, not the BMC adapter.
	features &^= bmc.FeatureSet(bmc.FeatureKVM)
	if h.HasKVM() {
		features |= bmc.FeatureSet(bmc.FeatureKVM)
	}
	return applyFeatureFlags(features, h.featureFlags)
}

// SetFeatureFlags applies inventory feature overrides (for tests and NewRegistry wiring).
func (h *Host) SetFeatureFlags(f *config.FeatureFlags) {
	if h != nil {
		h.featureFlags = f
	}
}

func applyFeatureFlags(set bmc.FeatureSet, f *config.FeatureFlags) bmc.FeatureSet {
	if f == nil {
		return set
	}
	if f.Sensors != nil && !*f.Sensors {
		set &^= bmc.FeatureSet(bmc.FeatureSensors)
	}
	if f.SEL != nil && !*f.SEL {
		set &^= bmc.FeatureSet(bmc.FeatureSEL)
	}
	if f.Power != nil && !*f.Power {
		set &^= bmc.FeatureSet(bmc.FeaturePower)
	}
	if f.Console != nil && !*f.Console {
		set &^= bmc.FeatureSet(bmc.FeatureConsole)
	}
	return set
}

// Registry is an ordered map of hosts built at startup.
type Registry struct {
	order     []string
	byID      map[string]*Host
	defaultID string
}

// Open builds clients for every inventory entry via the provider factory.
// Entries whose provider returns provider.ErrNotImplemented are skipped with a
// warning. Unknown providers and other factory errors fail Open. If defaultID
// is empty, the first successfully opened host becomes the default; if
// defaultID was set but skipped, Open fails.
func Open(cfgs []config.HostConfig, defaultID string, log *slog.Logger) (*Registry, error) {
	if log == nil {
		log = slog.Default()
	}
	if len(cfgs) == 0 {
		return nil, fmt.Errorf("hosts: empty inventory")
	}
	r := &Registry{
		byID:      make(map[string]*Host, len(cfgs)),
		defaultID: defaultID,
	}
	var skippedDefault bool
	for _, cfg := range cfgs {
		client, err := provider.New(cfg)
		if err != nil {
			if errors.Is(err, provider.ErrNotImplemented) {
				log.Warn("skipping unimplemented host",
					"id", cfg.ID,
					"provider", cfg.Provider,
					"err", err,
				)
				if defaultID != "" && cfg.ID == defaultID {
					skippedDefault = true
				}
				continue
			}
			_ = r.Close()
			return nil, err
		}
		h := &Host{
			ID:           cfg.ID,
			Name:         cfg.Name,
			Provider:     cfg.Provider,
			Address:      cfg.Host,
			Port:         cfg.Port,
			User:         cfg.User,
			Password:     cfg.Password,
			amtTLS:       cfg.AMTTLS(),
			iloInsecure:  cfg.ILOInsecureSkipVerify(),
			sensorNames:  cfg.SensorNames,
			featureFlags: cfg.Features,
			Client:       client,
		}
		if cfg.HasAMIKVM() {
			port, tls := cfg.KVMEndpoint()
			h.hasAMIKVM = true
			h.kvmPort = port
			h.kvmTLS = tls
		}
		if cfg.HasAMTKVM() {
			port, tls := cfg.AMTKVMEndpoint()
			h.hasAMTKVM = true
			h.kvmPort = port
			h.kvmTLS = tls
		}
		if cfg.ILORemoteConsole() {
			h.hasILOKVM = true
			if h.kvmPort == 0 {
				h.kvmPort = 17990 // typical iLO 4 rc_port; actual port comes from rc_info at connect
			}
		}
		r.order = append(r.order, cfg.ID)
		r.byID[cfg.ID] = h
	}
	if len(r.order) == 0 {
		_ = r.Close()
		return nil, fmt.Errorf("hosts: no usable hosts (all providers unimplemented or failed)")
	}
	if skippedDefault {
		_ = r.Close()
		return nil, fmt.Errorf("hosts: default host %q skipped: provider not implemented", defaultID)
	}
	if r.defaultID == "" {
		r.defaultID = r.order[0]
	}
	if _, ok := r.byID[r.defaultID]; !ok {
		_ = r.Close()
		return nil, fmt.Errorf("hosts: default host %q not found", r.defaultID)
	}
	return r, nil
}

// Get returns a host by id.
func (r *Registry) Get(id string) (*Host, error) {
	h, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("hosts: unknown host %q", id)
	}
	return h, nil
}

// Default returns the inventory default host (OUTBAND_DEFAULT_HOST or first usable).
func (r *Registry) Default() *Host {
	return r.byID[r.defaultID]
}

// DefaultID returns the id of Default.
func (r *Registry) DefaultID() string {
	if r == nil {
		return ""
	}
	return r.defaultID
}

// All returns hosts in inventory order.
func (r *Registry) All() []*Host {
	out := make([]*Host, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// NewRegistry builds a registry from already-open hosts (tests and advanced wiring).
func NewRegistry(list []*Host, defaultID string) (*Registry, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("hosts: empty registry")
	}
	r := &Registry{
		byID:      make(map[string]*Host, len(list)),
		defaultID: defaultID,
	}
	for _, h := range list {
		if h == nil || h.ID == "" {
			return nil, fmt.Errorf("hosts: nil or empty host id")
		}
		if _, ok := r.byID[h.ID]; ok {
			return nil, fmt.Errorf("hosts: duplicate id %q", h.ID)
		}
		r.order = append(r.order, h.ID)
		r.byID[h.ID] = h
	}
	if r.defaultID == "" {
		r.defaultID = r.order[0]
	}
	if _, ok := r.byID[r.defaultID]; !ok {
		return nil, fmt.Errorf("hosts: default host %q not found", r.defaultID)
	}
	return r, nil
}

// Close closes every host client.
func (r *Registry) Close() error {
	var first error
	for _, id := range r.order {
		h := r.byID[id]
		if h == nil || h.Client == nil {
			continue
		}
		if err := h.Client.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
