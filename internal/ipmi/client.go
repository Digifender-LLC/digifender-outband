// Package ipmi implements bmc.Client over IPMI 2.0 / RMCP+.
package ipmi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	goipmi "github.com/bougou/go-ipmi"

	"outband/internal/bmc"
)

// Config is the RMCP+ connection settings.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	CipherID int // -1 = library default
}

// Adapter implements bmc.Client and bmc.Console.
type Adapter struct {
	cfg Config

	mu     sync.Mutex
	client *goipmi.Client

	solMu     sync.Mutex
	solActive bool

	cacheMu      sync.Mutex
	mcInfoCache  *cachedMCInfo
	powerCache   *cachedPower
	sensorCache  *cachedSensors
	selCache     *cachedSEL
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

// New creates an IPMI adapter. Connection is established lazily.
func New(cfg Config) *Adapter {
	if cfg.Port == 0 {
		cfg.Port = 623
	}
	return &Adapter{cfg: cfg}
}

// Features reports the IPMI adapter capability set.
func (a *Adapter) Features() bmc.FeatureSet {
	return bmc.AllIPMIFeatures()
}

func (a *Adapter) ensureClient(ctx context.Context) (*goipmi.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	c, err := goipmi.NewClient(a.cfg.Host, a.cfg.Port, a.cfg.User, a.cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("new IPMI client: %w", err)
	}
	c.WithInterface(goipmi.InterfaceLanplus)
	if a.cfg.CipherID >= 0 {
		c.WithCipherSuiteID(goipmi.CipherSuiteID(a.cfg.CipherID))
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("IPMI connect to %s:%d: %w", a.cfg.Host, a.cfg.Port, err)
	}
	a.client = c
	return a.client, nil
}

func (a *Adapter) withClient(ctx context.Context, fn func(*goipmi.Client) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	c, err := a.ensureClient(ctx)
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		if sessionBroken(err) {
			_ = c.Close(context.Background())
			a.client = nil
		}
		return err
	}
	return nil
}

// sessionBroken reports whether err likely means the RMCP+ session is dead
// and should be dropped. Context cancellation is not treated as session death.
func sessionBroken(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "session") ||
		strings.Contains(msg, "not connected") ||
		strings.Contains(msg, "broken pipe")
}

// Close closes the shared IPMI session.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	err := a.client.Close(context.Background())
	a.client = nil
	return err
}

// MCInfo returns Get Device ID fields.
func (a *Adapter) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	a.cacheMu.Lock()
	if a.mcInfoCache != nil && time.Since(a.mcInfoCache.at) < 15*time.Second {
		info := *a.mcInfoCache.info
		a.cacheMu.Unlock()
		return &info, nil
	}
	a.cacheMu.Unlock()

	var out *bmc.MCInfo
	err := a.withClient(ctx, func(c *goipmi.Client) error {
		res, err := c.GetDeviceID(ctx)
		if err != nil {
			return err
		}
		out = &bmc.MCInfo{
			DeviceID:        res.DeviceID,
			FirmwareRev:     fmt.Sprintf("%d.%02d", res.MajorFirmwareRevision, res.MinorFirmwareRevision),
			ProtocolVersion: fmt.Sprintf("%d.%d", res.MajorIPMIVersion, res.MinorIPMIVersion),
			ManufacturerID:  res.ManufacturerID,
			Manufacturer:    goipmi.OEM(res.ManufacturerID).String(),
			ProductID:       res.ProductID,
		}
		return nil
	})
	if err == nil {
		a.cacheMu.Lock()
		a.mcInfoCache = &cachedMCInfo{at: time.Now(), info: out}
		a.cacheMu.Unlock()
	}
	return out, err
}

// PowerStatus returns chassis power state.
func (a *Adapter) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	a.cacheMu.Lock()
	if a.powerCache != nil && time.Since(a.powerCache.at) < 3*time.Second {
		st := *a.powerCache.status
		a.cacheMu.Unlock()
		return &st, nil
	}
	a.cacheMu.Unlock()

	var out *bmc.PowerStatus
	err := a.withClient(ctx, func(c *goipmi.Client) error {
		res, err := c.GetChassisStatus(ctx)
		if err != nil {
			return err
		}
		out = &bmc.PowerStatus{
			IsOn:               res.PowerIsOn,
			PowerRestorePolicy: res.PowerRestorePolicy.String(),
			PowerFault:         res.PowerFault,
			ChassisIntrusion:   res.ChassisIntrusionActive,
		}
		return nil
	})
	if err == nil {
		a.cacheMu.Lock()
		a.powerCache = &cachedPower{at: time.Now(), status: out}
		a.cacheMu.Unlock()
	}
	return out, err
}

// PowerControl issues a chassis control command.
func (a *Adapter) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	var control goipmi.ChassisControl
	switch action {
	case bmc.PowerOn:
		control = goipmi.ChassisControlPowerUp
	case bmc.PowerOff:
		control = goipmi.ChassisControlPowerDown
	case bmc.PowerCycle:
		control = goipmi.ChassisControlPowerCycle
	case bmc.PowerSoft:
		control = goipmi.ChassisControlSoftShutdown
	default:
		return fmt.Errorf("unknown power action %q", action)
	}
	return a.withClient(ctx, func(c *goipmi.Client) error {
		_, err := c.ChassisControl(ctx, control)
		if err == nil {
			a.cacheMu.Lock()
			a.powerCache = nil
			a.cacheMu.Unlock()
		}
		return err
	})
}

// Sensors returns SDR sensor readings.
func (a *Adapter) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	a.cacheMu.Lock()
	if a.sensorCache != nil && time.Since(a.sensorCache.at) < 8*time.Second {
		out := append([]bmc.Sensor(nil), a.sensorCache.sensors...)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	var out []bmc.Sensor
	err := a.withClient(ctx, func(c *goipmi.Client) error {
		sensors, err := c.GetSensors(ctx)
		if err != nil {
			return err
		}
		out = make([]bmc.Sensor, 0, len(sensors))
		for _, s := range sensors {
			if s == nil {
				continue
			}
			status := s.Status()
			if status == "" || status == "ok" {
				status = "ok"
			}
			value := s.ReadingStr()
			unit := s.SensorUnit.String()
			if !s.HasAnalogReading {
				unit = "discrete"
			}
			present := s.IsReadingValid() || status != "N/A"
			out = append(out, bmc.Sensor{
				ID:      fmt.Sprintf("%02x", s.Number),
				Name:    s.Name,
				Type:    s.SensorType.String(),
				Value:   value,
				Unit:    unit,
				Status:  status,
				Present: present,
			})
		}
		return nil
	})
	if err == nil {
		a.cacheMu.Lock()
		a.sensorCache = &cachedSensors{at: time.Now(), sensors: out}
		a.cacheMu.Unlock()
	}
	return out, err
}

// SEL returns recent system event log entries (newest first), capped by limit.
func (a *Adapter) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	a.cacheMu.Lock()
	if a.selCache != nil && time.Since(a.selCache.at) < 12*time.Second {
		entries := a.selCache.entries
		if len(entries) > limit {
			entries = entries[:limit]
		}
		out := append([]bmc.SELEntry(nil), entries...)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	var out []bmc.SELEntry
	err := a.withClient(ctx, func(c *goipmi.Client) error {
		records, err := c.GetSELEntries(ctx, 0)
		if err != nil {
			return err
		}
		for i := len(records) - 1; i >= 0 && len(out) < 100; i-- {
			sel := records[i]
			if sel == nil || sel.Standard == nil {
				continue
			}
			s := sel.Standard
			out = append(out, bmc.SELEntry{
				ID:          fmt.Sprintf("%04x", sel.RecordID),
				Timestamp:   s.Timestamp,
				SensorType:  s.SensorType.String(),
				SensorName:  fmt.Sprintf("%s (#%02x)", s.SensorType.String(), s.SensorNumber),
				Description: s.EventString(),
				Direction:   s.EventDir.String(),
				Severity:    string(s.EventSeverity()),
			})
		}
		return nil
	})
	if err == nil {
		a.cacheMu.Lock()
		a.selCache = &cachedSEL{at: time.Now(), entries: out}
		a.cacheMu.Unlock()
		if len(out) > limit {
			out = out[:limit]
		}
	}
	return out, err
}

// OpenSOL opens a dedicated RMCP+ session and activates SOL.
// Only one SOL session is allowed per process.
func (a *Adapter) OpenSOL(ctx context.Context) (bmc.SOLSession, error) {
	a.solMu.Lock()
	if a.solActive {
		a.solMu.Unlock()
		return nil, fmt.Errorf("SOL session already active: %w", bmc.ErrBusy)
	}
	a.solActive = true
	a.solMu.Unlock()

	release := func() {
		a.solMu.Lock()
		a.solActive = false
		a.solMu.Unlock()
	}

	c, err := goipmi.NewClient(a.cfg.Host, a.cfg.Port, a.cfg.User, a.cfg.Password)
	if err != nil {
		release()
		return nil, fmt.Errorf("new SOL IPMI client: %w", err)
	}
	c.WithInterface(goipmi.InterfaceLanplus)
	if a.cfg.CipherID >= 0 {
		c.WithCipherSuiteID(goipmi.CipherSuiteID(a.cfg.CipherID))
	}

	solCtx, cancel := context.WithCancel(ctx)
	if err := c.Connect(solCtx); err != nil {
		cancel()
		release()
		return nil, fmt.Errorf("SOL connect: %w", err)
	}

	bmcOutR, bmcOutW := io.Pipe()
	bmcInR, bmcInW := io.Pipe()

	ready := make(chan error, 1)
	done := make(chan struct{})

	sess := &solSession{
		reader: bmcOutR,
		writer: bmcInW,
		done:   done,
		onClose: func() {
			cancel()
			_ = bmcInW.Close()
			_ = bmcOutR.Close()
			<-done
			_ = bmcOutW.Close()
			_ = bmcInR.Close()
			_ = c.Close(context.Background())
			release()
		},
	}

	go func() {
		defer close(done)
		opts := &goipmi.SOLActivateOptions{
			OnActivated: func(uint8, io.Reader, io.Writer, *goipmi.ActivatePayloadResponse) {
				select {
				case ready <- nil:
				default:
				}
			},
			OnDeactivated: func(uint8, io.Reader, io.Writer, *goipmi.ActivatePayloadResponse) {},
		}
		err := c.SOLActivate(solCtx, bmcInR, bmcOutW, opts)
		select {
		case ready <- err:
		default:
		}
		_ = bmcOutW.CloseWithError(errOrEOF(err))
		_ = bmcInR.CloseWithError(errOrEOF(err))
	}()

	select {
	case err := <-ready:
		if err != nil {
			sess.Close()
			return nil, fmt.Errorf("SOL activate: %w", err)
		}
	case <-time.After(15 * time.Second):
		sess.Close()
		return nil, fmt.Errorf("SOL activate timed out")
	case <-solCtx.Done():
		sess.Close()
		return nil, solCtx.Err()
	}

	return sess, nil
}

func errOrEOF(err error) error {
	if err == nil || err == context.Canceled || err == io.EOF {
		return io.EOF
	}
	return err
}

type solSession struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	done    chan struct{}
	onClose func()
	once    sync.Once
}

func (s *solSession) Read(p []byte) (int, error)  { return s.reader.Read(p) }
func (s *solSession) Write(p []byte) (int, error) { return s.writer.Write(p) }

func (s *solSession) Close() error {
	s.once.Do(s.onClose)
	return nil
}
