// Package examplehost is a copy-paste skeleton for in-tree BMC providers.
//
// It is intentionally NOT blank-imported from cmd/mipmi. Import it from tests
// or add `_ "outband/internal/examplehost"` in main when wiring a real backend
// modeled on this package. See docs/providers.md.
package examplehost

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/provider"
)

const Name = "examplehost"

func init() {
	provider.Register(Name, func(cfg config.HostConfig) (bmc.Client, error) {
		return New(cfg)
	})
}

// Options are decoded from inventory options.examplehost.
type Options struct {
	// Model overrides the MCInfo model string when set.
	Model string `json:"model,omitempty"`
	// PoweredOn is the simulated chassis power state (default true).
	PoweredOn *bool `json:"powered_on,omitempty"`
}

// Client is a minimal bmc.Client + Capabilities (identity + power only).
type Client struct {
	addr string
	opts Options

	mu   sync.Mutex
	isOn bool
}

// New builds a Client from host inventory. Decodes options.examplehost when present.
func New(cfg config.HostConfig) (*Client, error) {
	c := &Client{
		addr: cfg.Host,
		isOn: true,
	}
	if raw, ok := cfg.ProviderOptions(Name); ok {
		if err := json.Unmarshal(raw, &c.opts); err != nil {
			return nil, fmt.Errorf("%s options: %w", Name, err)
		}
	}
	if c.opts.PoweredOn != nil {
		c.isOn = *c.opts.PoweredOn
	}
	return c, nil
}

// Features advertises only identity and power — omit unsupported bits.
func (c *Client) Features() bmc.FeatureSet {
	return bmc.FeatureSet(bmc.FeatureIdentity | bmc.FeaturePower)
}

func (c *Client) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	_ = ctx
	model := c.opts.Model
	if model == "" {
		model = "examplehost"
	}
	return &bmc.MCInfo{
		FirmwareRev:     "0.0.0",
		ProtocolVersion: "example",
		Manufacturer:    "mIPMI",
		Model:           model,
	}, nil
}

func (c *Client) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	return &bmc.PowerStatus{IsOn: c.isOn}, nil
}

func (c *Client) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case bmc.PowerOn:
		c.isOn = true
	case bmc.PowerOff, bmc.PowerSoft:
		c.isOn = false
	case bmc.PowerCycle:
		c.isOn = true
	default:
		return fmt.Errorf("%s: %w", action, bmc.ErrUnsupported)
	}
	return nil
}

func (c *Client) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	_ = ctx
	return nil, bmc.ErrUnsupported
}

func (c *Client) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	_ = ctx
	_ = limit
	return nil, bmc.ErrUnsupported
}

func (c *Client) Close() error { return nil }
