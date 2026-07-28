// Package provider registers BMC connection factories by provider name.
package provider

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"outband/internal/bmc"
	"outband/internal/config"
)

// ErrNotImplemented is returned by stub providers that are not yet wired.
var ErrNotImplemented = errors.New("provider not implemented")

// Factory builds a bmc.Client from a host inventory entry.
type Factory func(cfg config.HostConfig) (bmc.Client, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register associates a provider name with a factory. Panics on duplicate names.
func Register(name string, f Factory) {
	if name == "" {
		panic("provider: empty name")
	}
	if f == nil {
		panic("provider: nil factory for " + name)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := factories[name]; ok {
		panic("provider: duplicate registration for " + name)
	}
	factories[name] = f
}

// New constructs a Client for the given host config using the registered factory.
func New(cfg config.HostConfig) (bmc.Client, error) {
	mu.RLock()
	f, ok := factories[cfg.Provider]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider %q for host %q", cfg.Provider, cfg.ID)
	}
	client, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("provider %q host %q: %w", cfg.Provider, cfg.ID, err)
	}
	return client, nil
}

// Names returns registered provider names in sorted order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for name := range factories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Known reports whether a provider name is registered.
func Known(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := factories[name]
	return ok
}
