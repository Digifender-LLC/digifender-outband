package telemetry

import (
	"context"
	"log/slog"
	"time"

	"mipmi/internal/bmc"
)

// PollIntervals controls how often each BMC query runs.
type PollIntervals struct {
	Sensors time.Duration
	Power   time.Duration
	SEL     time.Duration
	MCInfo  time.Duration
	Prune   time.Duration
}

// DefaultIntervals returns plan defaults.
func DefaultIntervals() PollIntervals {
	return PollIntervals{
		Sensors: 10 * time.Second,
		Power:   5 * time.Second,
		SEL:     60 * time.Second,
		MCInfo:  5 * time.Minute,
		Prune:   time.Hour,
	}
}

// Collector polls one BMC host and writes into Store under HostID.
type Collector struct {
	HostID    string
	Client    bmc.Client
	Store     *Store
	Intervals PollIntervals
	Retention time.Duration
	Log       *slog.Logger
}

// Run blocks until ctx is cancelled. It performs an immediate warm poll first.
func (c *Collector) Run(ctx context.Context) {
	if c.Log == nil {
		c.Log = slog.Default()
	}
	iv := c.Intervals
	if iv.Sensors <= 0 {
		iv.Sensors = 10 * time.Second
	}
	if iv.Power <= 0 {
		iv.Power = 5 * time.Second
	}
	if iv.SEL <= 0 {
		iv.SEL = 60 * time.Second
	}
	if iv.MCInfo <= 0 {
		iv.MCInfo = 5 * time.Minute
	}
	if iv.Prune <= 0 {
		iv.Prune = time.Hour
	}
	if c.Retention <= 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	if c.HostID == "" {
		c.HostID = "default"
	}

	features := bmc.ClientFeatures(c.Client)

	_ = c.Store.LoadSnapshots(c.HostID)

	// Warm immediately so post-login HTMX can hit a filled store.
	if features.Has(bmc.FeatureIdentity) {
		c.pollMCInfo(ctx)
	}
	if features.Has(bmc.FeaturePower) {
		c.pollPower(ctx)
	}
	if features.Has(bmc.FeatureSensors) {
		c.pollSensors(ctx)
	}
	if features.Has(bmc.FeatureSEL) {
		c.pollSEL(ctx)
	}

	sensorsT := time.NewTicker(iv.Sensors)
	powerT := time.NewTicker(iv.Power)
	selT := time.NewTicker(iv.SEL)
	mcT := time.NewTicker(iv.MCInfo)
	pruneT := time.NewTicker(iv.Prune)
	defer sensorsT.Stop()
	defer powerT.Stop()
	defer selT.Stop()
	defer mcT.Stop()
	defer pruneT.Stop()

	c.Log.Info("telemetry collector running",
		"host", c.HostID,
		"features", uint64(features),
		"sensors", iv.Sensors,
		"power", iv.Power,
		"sel", iv.SEL,
		"mcinfo", iv.MCInfo,
		"retention", c.Retention,
	)

	for {
		select {
		case <-ctx.Done():
			c.Log.Info("telemetry collector stopped", "host", c.HostID)
			return
		case <-sensorsT.C:
			if features.Has(bmc.FeatureSensors) {
				c.pollSensors(ctx)
			}
		case <-powerT.C:
			if features.Has(bmc.FeaturePower) {
				c.pollPower(ctx)
			}
		case <-selT.C:
			if features.Has(bmc.FeatureSEL) {
				c.pollSEL(ctx)
			}
		case <-mcT.C:
			if features.Has(bmc.FeatureIdentity) {
				c.pollMCInfo(ctx)
			}
		case <-pruneT.C:
			if err := c.Store.Prune(c.Retention); err != nil {
				c.Log.Warn("telemetry prune", "err", err)
			}
		}
	}
}

func (c *Collector) pollMCInfo(ctx context.Context) {
	info, err := c.Client.MCInfo(ctx)
	if err != nil {
		c.Store.SetError(c.HostID, err.Error())
		c.Log.Warn("poll mcinfo", "host", c.HostID, "err", err)
		return
	}
	if err := c.Store.RecordMCInfo(c.HostID, info); err != nil {
		c.Log.Warn("record mcinfo", "host", c.HostID, "err", err)
	}
}

func (c *Collector) pollPower(ctx context.Context) {
	ps, err := c.Client.PowerStatus(ctx)
	if err != nil {
		c.Store.SetError(c.HostID, err.Error())
		c.Log.Warn("poll power", "host", c.HostID, "err", err)
		return
	}
	if err := c.Store.RecordPower(c.HostID, ps); err != nil {
		c.Log.Warn("record power", "host", c.HostID, "err", err)
	}
}

func (c *Collector) pollSensors(ctx context.Context) {
	sensors, err := c.Client.Sensors(ctx)
	if err != nil {
		c.Store.SetError(c.HostID, err.Error())
		c.Log.Warn("poll sensors", "host", c.HostID, "err", err)
		return
	}
	if err := c.Store.RecordSensors(c.HostID, sensors); err != nil {
		c.Log.Warn("record sensors", "host", c.HostID, "err", err)
	}
}

func (c *Collector) pollSEL(ctx context.Context) {
	entries, err := c.Client.SEL(ctx, 80)
	if err != nil {
		c.Store.SetError(c.HostID, err.Error())
		c.Log.Warn("poll sel", "host", c.HostID, "err", err)
		return
	}
	if err := c.Store.RecordSEL(c.HostID, entries); err != nil {
		c.Log.Warn("record sel", "host", c.HostID, "err", err)
	}
}
