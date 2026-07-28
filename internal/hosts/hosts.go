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
	ID       string
	Name     string
	Provider string
	Address  string
	User     string
	Password string // server-side only; used for AMI web / KVM
	hasKVM   bool
	kvmPort  int
	kvmTLS   bool
	// sensorNames maps SDR names → display labels (from inventory sensor_names).
	sensorNames map[string]string
	Client      bmc.Client
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

// HasKVM reports whether AMI KVM is configured for this host.
func (h *Host) HasKVM() bool {
	return h != nil && h.hasKVM
}

// KVMPort returns the IVTP video port (meaningful when HasKVM).
func (h *Host) KVMPort() int {
	if h == nil {
		return 0
	}
	return h.kvmPort
}

// KVMTLS reports whether the IVTP socket uses TLS (meaningful when HasKVM).
func (h *Host) KVMTLS() bool {
	return h != nil && h.kvmTLS
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
			ID:          cfg.ID,
			Name:        cfg.Name,
			Provider:    cfg.Provider,
			Address:     cfg.Host,
			User:        cfg.User,
			Password:    cfg.Password,
			sensorNames: cfg.SensorNames,
			Client:      client,
		}
		if cfg.HasKVM() {
			port, tls := cfg.KVMEndpoint()
			h.hasKVM = true
			h.kvmPort = port
			h.kvmTLS = tls
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

// Default returns the active host for this process.
func (r *Registry) Default() *Host {
	return r.byID[r.defaultID]
}

// All returns hosts in inventory order.
func (r *Registry) All() []*Host {
	out := make([]*Host, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
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
