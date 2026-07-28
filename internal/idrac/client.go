package idrac

import (
	"context"
	"sync"
	"time"

	"outband/internal/bmc"
)

// Config is the connection settings for a Dell iDRAC host.
//
// Transport selects the wire protocol. Empty / "auto" probes the BMC once per
// Adapter (Redfish → web → WS-MAN). Each inventory host gets its own Adapter,
// so one Outband process can mix iDRAC7 (web/WS-MAN) and iDRAC9 (Redfish).
type Config struct {
	Host               string
	Port               int // 0 → 443
	User               string
	Password           string
	InsecureSkipVerify bool
	// Transport is auto|redfish|wsman|web.
	Transport string
}

// Adapter implements bmc.Client for one iDRAC. Backend resolution is lazy and
// cached for the lifetime of the Adapter.
type Adapter struct {
	cfg Config

	resolveOnce sync.Once
	resolveErr  error
	backend     backend
	backendName string

	cacheMu     sync.Mutex
	mcInfoCache *cachedMCInfo
	powerCache  *cachedPower
	sensorCache *cachedSensors
	selCache    *cachedSEL
}

type cachedMCInfo struct {
	at   time.Time
	info *bmc.MCInfo
}
type cachedPower struct {
	at     time.Time
	status *bmc.PowerStatus
}
type cachedSensors struct {
	at      time.Time
	sensors []bmc.Sensor
}
type cachedSEL struct {
	at      time.Time
	entries []bmc.SELEntry
}

// New creates an iDRAC adapter. Detection and login happen on first BMC call.
func New(cfg Config) *Adapter {
	cfg.Transport = normalizeTransport(cfg.Transport)
	return &Adapter{cfg: cfg}
}

// Features reports the iDRAC adapter capability set (no console/KVM in v1).
// Classic web transport does not expose a usable SEL API, so FeatureSEL is
// omitted when transport is web (configured or resolved).
func (a *Adapter) Features() bmc.FeatureSet {
	f := bmc.FeatureSet(bmc.FeaturePower | bmc.FeatureSensors | bmc.FeatureIdentity)
	if !a.webTransport() {
		f |= bmc.FeatureSet(bmc.FeatureSEL)
	}
	return f
}

func (a *Adapter) webTransport() bool {
	if a.backendName == TransportWeb {
		return true
	}
	return a.cfg.Transport == TransportWeb
}

// TransportName returns the resolved backend name, or the configured preference
// before the first successful resolve.
func (a *Adapter) TransportName() string {
	if a.backendName != "" {
		return a.backendName
	}
	return a.cfg.Transport
}

func (a *Adapter) ensureBackend(ctx context.Context) error {
	a.resolveOnce.Do(func() {
		if a.backend != nil {
			a.backendName = a.backend.Name()
			return
		}
		b, err := resolveBackend(ctx, a.cfg)
		if err != nil {
			a.resolveErr = err
			return
		}
		a.backend = b
		a.backendName = b.Name()
	})
	return a.resolveErr
}

// Close releases the active backend.
func (a *Adapter) Close() error {
	if a.backend != nil {
		return a.backend.Close()
	}
	return nil
}

// MCInfo returns iDRAC/system identity.
func (a *Adapter) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	if err := a.ensureBackend(ctx); err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	if a.mcInfoCache != nil && time.Since(a.mcInfoCache.at) < 15*time.Second {
		info := *a.mcInfoCache.info
		a.cacheMu.Unlock()
		return &info, nil
	}
	a.cacheMu.Unlock()

	info, err := a.backend.MCInfo(ctx)
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.mcInfoCache = &cachedMCInfo{at: time.Now(), info: info}
	a.cacheMu.Unlock()
	cp := *info
	return &cp, nil
}

// PowerStatus reads chassis power state.
func (a *Adapter) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	if err := a.ensureBackend(ctx); err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	if a.powerCache != nil && time.Since(a.powerCache.at) < 3*time.Second {
		st := *a.powerCache.status
		a.cacheMu.Unlock()
		return &st, nil
	}
	a.cacheMu.Unlock()

	status, err := a.backend.PowerStatus(ctx)
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.powerCache = &cachedPower{at: time.Now(), status: status}
	a.cacheMu.Unlock()
	cp := *status
	return &cp, nil
}

// PowerControl requests a power action.
func (a *Adapter) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	if err := a.ensureBackend(ctx); err != nil {
		return err
	}
	if err := a.backend.PowerControl(ctx, action); err != nil {
		return err
	}
	a.cacheMu.Lock()
	a.powerCache = nil
	a.cacheMu.Unlock()
	return nil
}

// Sensors returns thermal / health readings.
func (a *Adapter) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	if err := a.ensureBackend(ctx); err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	if a.sensorCache != nil && time.Since(a.sensorCache.at) < 8*time.Second {
		out := append([]bmc.Sensor(nil), a.sensorCache.sensors...)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	out, err := a.backend.Sensors(ctx)
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.sensorCache = &cachedSensors{at: time.Now(), sensors: out}
	a.cacheMu.Unlock()
	return append([]bmc.Sensor(nil), out...), nil
}

// SEL returns system event log entries (newest first when IDs are numeric).
func (a *Adapter) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if err := a.ensureBackend(ctx); err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	if a.selCache != nil && time.Since(a.selCache.at) < 12*time.Second {
		out := truncateSEL(a.selCache.entries, limit)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	entries, err := a.backend.SEL(ctx, limit)
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.selCache = &cachedSEL{at: time.Now(), entries: entries}
	a.cacheMu.Unlock()
	return truncateSEL(entries, limit), nil
}

var (
	_ bmc.Client       = (*Adapter)(nil)
	_ bmc.Capabilities = (*Adapter)(nil)
)
