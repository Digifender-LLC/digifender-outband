package telemetry

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"mipmi/internal/bmc"
)

// countingClient records how often each Client method is invoked.
type countingClient struct {
	features bmc.FeatureSet
	mcInfo   atomic.Int32
	power    atomic.Int32
	sensors  atomic.Int32
	sel      atomic.Int32
}

func (c *countingClient) Features() bmc.FeatureSet { return c.features }

func (c *countingClient) MCInfo(context.Context) (*bmc.MCInfo, error) {
	c.mcInfo.Add(1)
	return &bmc.MCInfo{FirmwareRev: "1.0"}, nil
}

func (c *countingClient) PowerStatus(context.Context) (*bmc.PowerStatus, error) {
	c.power.Add(1)
	return &bmc.PowerStatus{IsOn: true}, nil
}

func (c *countingClient) PowerControl(context.Context, bmc.PowerAction) error { return nil }

func (c *countingClient) Sensors(context.Context) ([]bmc.Sensor, error) {
	c.sensors.Add(1)
	return nil, nil
}

func (c *countingClient) SEL(context.Context, int) ([]bmc.SELEntry, error) {
	c.sel.Add(1)
	return nil, nil
}

func (c *countingClient) Close() error { return nil }

func TestCollectorSkipsUnsupportedFeatures(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := &countingClient{
		features: bmc.FeatureSet(bmc.FeaturePower | bmc.FeatureIdentity),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Collector{
		HostID: "test",
		Client: client,
		Store:  store,
		Intervals: PollIntervals{
			Sensors: time.Hour,
			Power:   time.Hour,
			SEL:     time.Hour,
			MCInfo:  time.Hour,
			Prune:   time.Hour,
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	// Warm poll runs synchronously at start; give it a moment then stop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.power.Load() >= 1 && client.mcInfo.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collector did not stop")
	}

	if n := client.power.Load(); n < 1 {
		t.Fatalf("power polls = %d, want >= 1", n)
	}
	if n := client.mcInfo.Load(); n < 1 {
		t.Fatalf("mcinfo polls = %d, want >= 1", n)
	}
	if n := client.sensors.Load(); n != 0 {
		t.Fatalf("sensors polls = %d, want 0", n)
	}
	if n := client.sel.Load(); n != 0 {
		t.Fatalf("sel polls = %d, want 0", n)
	}
}
