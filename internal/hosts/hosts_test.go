package hosts

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/provider"
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
		{ID: "dell", Provider: "unimplemented", Host: "10.0.0.1", Password: "x"},
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
		{ID: "a", Provider: "unimplemented", Host: "10.0.0.1", Password: "x"},
		{ID: "b", Provider: "unimplemented", Host: "10.0.0.2", Password: "x"},
	}
	_, err := Open(cfgs, "", discardLog())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenDefaultSkipped(t *testing.T) {
	cfgs := []config.HostConfig{
		{ID: "dell", Provider: "unimplemented", Host: "10.0.0.1", Password: "x"},
		{ID: "ok", Provider: "testok", Host: "10.0.0.2", Password: "x"},
	}
	_, err := Open(cfgs, "dell", discardLog())
	if err == nil {
		t.Fatal("expected error when default is unimplemented")
	}
}

func TestRenameSensor(t *testing.T) {
	cfgs := []config.HostConfig{
		{
			ID:       "ok",
			Provider: "testok",
			Host:     "10.0.0.2",
			Password: "x",
			SensorNames: map[string]string{
				"CPU DTS value": "CPU temperature",
			},
		},
	}
	r, err := Open(cfgs, "ok", discardLog())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	h := r.Default()
	if got := h.RenameSensor("CPU DTS value"); got != "CPU temperature" {
		t.Fatalf("got %q", got)
	}
	if got := h.RenameSensor("Other"); got != "Other" {
		t.Fatalf("passthrough %q", got)
	}
}

func TestFeaturesDisableSensors(t *testing.T) {
	off := false
	cfgs := []config.HostConfig{
		{
			ID:       "ok",
			Provider: "testok",
			Host:     "10.0.0.2",
			Password: "x",
			Features: &config.FeatureFlags{Sensors: &off},
		},
	}
	// testok has no Capabilities → control-plane defaults include sensors.
	r, err := Open(cfgs, "ok", discardLog())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f := r.Default().Features()
	if f.Has(bmc.FeatureSensors) {
		t.Fatal("expected sensors disabled by inventory")
	}
	if !f.Has(bmc.FeaturePower) {
		t.Fatal("expected power still enabled")
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
		{ID: "dell", Provider: "unimplemented", Host: "10.0.0.1", Password: "x"},
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

func TestOpenKVMFlags(t *testing.T) {
	cfgs := []config.HostConfig{
		{
			ID: "ami", Provider: "testok", Host: "10.0.0.1", Password: "x",
			KVM: &config.KVMOptions{Port: 7578},
		},
		{
			ID: "amt", Provider: "testok", Host: "10.0.0.2", Password: "x",
			AMT: &config.AMTOptions{KVM: &config.AMTKVMOptions{}},
		},
	}
	r, err := Open(cfgs, "ami", discardLog())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ami, err := r.Get("ami")
	if err != nil {
		t.Fatal(err)
	}
	if !ami.HasAMIKVM() || ami.HasAMTKVM() || ami.HasILOKVM() {
		t.Fatalf("ami flags: ami=%v amt=%v ilo=%v", ami.HasAMIKVM(), ami.HasAMTKVM(), ami.HasILOKVM())
	}
	if !ami.HasKVM() || !ami.Features().Has(bmc.FeatureKVM) {
		t.Fatal("ami must advertise FeatureKVM")
	}
	if ami.KVMPort() != 7578 {
		t.Fatalf("ami port=%d", ami.KVMPort())
	}

	amt, err := r.Get("amt")
	if err != nil {
		t.Fatal(err)
	}
	if !amt.HasAMTKVM() || amt.HasAMIKVM() || amt.HasILOKVM() {
		t.Fatalf("amt flags: ami=%v amt=%v ilo=%v", amt.HasAMIKVM(), amt.HasAMTKVM(), amt.HasILOKVM())
	}
	if !amt.HasKVM() || !amt.Features().Has(bmc.FeatureKVM) {
		t.Fatal("amt must advertise FeatureKVM")
	}
	if amt.KVMPort() != 16994 {
		t.Fatalf("amt port=%d want 16994", amt.KVMPort())
	}
}

func TestOpenILOKVMFlag(t *testing.T) {
	// Avoid live iLO: set hasILOKVM the same way Open does for provider ilo.
	h := &Host{ID: "ilo", hasILOKVM: true, kvmPort: 17990}
	if !h.HasILOKVM() || !h.HasKVM() || h.HasAMIKVM() || h.HasAMTKVM() {
		t.Fatalf("ilo flags wrong: ami=%v amt=%v ilo=%v", h.HasAMIKVM(), h.HasAMTKVM(), h.HasILOKVM())
	}
	h.Client = &okClient{}
	if !h.Features().Has(bmc.FeatureKVM) {
		t.Fatal("ilo host must advertise FeatureKVM")
	}
}
