// Package hosts manages the in-process BMC host registry.
package hosts

import (
	"fmt"

	"mipmi/internal/bmc"
	"mipmi/internal/config"
	"mipmi/internal/provider"
)

// Host is a registered BMC with an open client.
type Host struct {
	ID       string
	Name     string
	Provider string
	Address  string
	User     string
	Password string // server-side only; used for AMI web / KVM
	KVMPort  int    // IVTP video port (default 7578)
	KVMTLS  bool   // wrap video socket in TLS
	Client   bmc.Client
}

// DisplayName returns a UI label (name if set, else address).
func (h *Host) DisplayName() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Address
}

// Registry is an ordered map of hosts built at startup.
type Registry struct {
	order     []string
	byID      map[string]*Host
	defaultID string
}

// Open builds clients for every inventory entry via the provider factory.
func Open(cfgs []config.HostConfig, defaultID string) (*Registry, error) {
	if len(cfgs) == 0 {
		return nil, fmt.Errorf("hosts: empty inventory")
	}
	r := &Registry{
		byID:      make(map[string]*Host, len(cfgs)),
		defaultID: defaultID,
	}
	for _, cfg := range cfgs {
		client, err := provider.New(cfg)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		kvmPort := cfg.KVMPort
		if kvmPort == 0 {
			kvmPort = 7578
		}
		h := &Host{
			ID:       cfg.ID,
			Name:     cfg.Name,
			Provider: cfg.Provider,
			Address:  cfg.Host,
			User:     cfg.User,
			Password: cfg.Password,
			KVMPort:  kvmPort,
			KVMTLS:  cfg.KVMTLS,
			Client:   client,
		}
		r.order = append(r.order, cfg.ID)
		r.byID[cfg.ID] = h
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
