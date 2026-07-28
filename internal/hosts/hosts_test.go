package hosts

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"mipmi/internal/bmc"
	"mipmi/internal/config"
	"mipmi/internal/provider"
)

func init() {
	provider.Register("testok", func(cfg config.HostConfig) (bmc.Client, error) {
		return &okClient{}, nil
	})
}

type okClient struct{}

func (okClient) MCInfo(context.Context) (*bmc.MCInfo, error)           { return &bmc.MCInfo{}, nil }
func (okClient) PowerStatus(context.Context) (*bmc.PowerStatus, error) { return &bmc.PowerStatus{}, nil }
func (okClient) PowerControl(context.Context, bmc.PowerAction) error   { return nil }
func (okClient) Sensors(context.Context) ([]bmc.Sensor, error)         { return nil, nil }
func (okClient) SEL(context.Context, int) ([]bmc.SELEntry, error)      { return nil, nil }
func (okClient) Close() error                                          { return nil }

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpenSkipsUnimplemented(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "dell", Provider: "idrac", Host: "10.0.0.1", Password: "x"},
		{ID: "ok", Provider: "testok", Host: "10.0.0.2", Password: "x"},
	}
	r, err := Open(cfgs, "ok", discardLog())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.All()) != 1 {
		t.Fatalf("hosts = %d, want 1", len(r.All()))
	}
	if r.Default().ID != "ok" {
		t.Fatalf("default = %q, want ok", r.Default().ID)
	}
}

func TestOpenAllUnimplemented(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "a", Provider: "idrac", Host: "10.0.0.1", Password: "x"},
		{ID: "b", Provider: "amt", Host: "10.0.0.2", Password: "x"},
	}
	_, err := Open(cfgs, "", discardLog())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenDefaultSkipped(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "dell", Provider: "idrac", Host: "10.0.0.1", Password: "x"},
		{ID: "ok", Provider: "testok", Host: "10.0.0.2", Password: "x"},
	}
	_, err := Open(cfgs, "dell", discardLog())
	if err == nil {
		t.Fatal("expected error when default is unimplemented")
	}
}

func TestOpenUnknownProvider(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "x", Provider: "nope", Host: "10.0.0.1", Password: "x"},
	}
	_, err := Open(cfgs, "", discardLog())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenEmptyDefaultUsesFirstUsable(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "dell", Provider: "idrac", Host: "10.0.0.1", Password: "x"},
		{ID: "ok", Provider: "testok", Host: "10.0.0.2", Password: "x"},
	}
	r, err := Open(cfgs, "", discardLog())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Default().ID != "ok" {
		t.Fatalf("default = %q, want ok", r.Default().ID)
	}
}
