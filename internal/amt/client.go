package amt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"outband/internal/bmc"
)

// Config is the WS-MAN connection settings for Intel AMT.
type Config struct {
	Host     string
	Port     int  // 0 → 16992 (or 16993 when TLS)
	User     string
	Password string
	TLS      bool
}

// Adapter implements bmc.Client over AMT WS-MAN (HTTP Digest).
type Adapter struct {
	cfg Config
	ws  *wsmanClient

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

// New creates an AMT adapter. Connection is established lazily on first call.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, ws: newWSMAN(cfg)}
}

// Features reports the AMT adapter capability set (no console/KVM in v1).
func (a *Adapter) Features() bmc.FeatureSet {
	return bmc.FeatureSet(bmc.FeaturePower | bmc.FeatureSensors | bmc.FeatureSEL | bmc.FeatureIdentity)
}

// Close releases idle HTTP connections.
func (a *Adapter) Close() error {
	if a.ws != nil && a.ws.http != nil {
		a.ws.http.CloseIdleConnections()
	}
	return nil
}

// MCInfo returns AMT firmware identity from CIM_SoftwareIdentity.
func (a *Adapter) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	a.cacheMu.Lock()
	if a.mcInfoCache != nil && time.Since(a.mcInfoCache.at) < 15*time.Second {
		info := *a.mcInfoCache.info
		a.cacheMu.Unlock()
		return &info, nil
	}
	a.cacheMu.Unlock()

	data, err := a.ws.enumeratePull(ctx, uriCIMSoftwareID)
	if err != nil {
		return nil, err
	}
	items := eachNamedElement(data, "CIM_SoftwareIdentity")
	info := &bmc.MCInfo{
		Manufacturer:    "Intel",
		Model:           "AMT",
		ProtocolVersion: "WS-MAN",
	}
	for _, it := range items {
		id := it["InstanceID"]
		ver := it["VersionString"]
		switch id {
		case "AMT", "Flash", "AMT FW Core Version":
			if info.FirmwareRev == "" && ver != "" {
				info.FirmwareRev = ver
			}
		case "Build Number":
			if info.FirmwareRev != "" && ver != "" && !strings.Contains(info.FirmwareRev, ".") {
				info.FirmwareRev = info.FirmwareRev + "." + ver
			} else if info.FirmwareRev != "" && ver != "" && !strings.HasSuffix(info.FirmwareRev, ver) {
				// Prefer "12.0.95.2489" style when we already have 12.0.95
				parts := strings.Split(info.FirmwareRev, ".")
				if len(parts) == 3 {
					info.FirmwareRev = info.FirmwareRev + "." + ver
				}
			}
		case "Sku":
			info.Model = "AMT SKU " + ver
		}
	}
	if info.FirmwareRev == "" {
		info.FirmwareRev = "unknown"
	}

	a.cacheMu.Lock()
	a.mcInfoCache = &cachedMCInfo{at: time.Now(), info: info}
	a.cacheMu.Unlock()
	cp := *info
	return &cp, nil
}

// PowerStatus reads CIM_AssociatedPowerManagementService.PowerState.
func (a *Adapter) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	a.cacheMu.Lock()
	if a.powerCache != nil && time.Since(a.powerCache.at) < 3*time.Second {
		st := *a.powerCache.status
		a.cacheMu.Unlock()
		return &st, nil
	}
	a.cacheMu.Unlock()

	data, err := a.ws.enumeratePull(ctx, uriCIMPowerAssoc)
	if err != nil {
		return nil, err
	}
	ps := firstLocalText(data, "PowerState")
	n, _ := strconv.Atoi(ps)
	// CIM PowerState 2 = On; 8 = Off Soft; others treated as off.
	status := &bmc.PowerStatus{IsOn: n == 2}

	a.cacheMu.Lock()
	a.powerCache = &cachedPower{at: time.Now(), status: status}
	a.cacheMu.Unlock()
	cp := *status
	return &cp, nil
}

// PowerControl invokes RequestPowerStateChange.
func (a *Adapter) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	var state int
	switch action {
	case bmc.PowerOn:
		state = 2
	case bmc.PowerOff, bmc.PowerSoft:
		state = 8
	case bmc.PowerCycle:
		state = 5
	default:
		return fmt.Errorf("%w: power action %q", bmc.ErrUnsupported, action)
	}
	if err := a.ws.requestPowerState(ctx, state); err != nil {
		return err
	}
	a.cacheMu.Lock()
	a.powerCache = nil
	a.cacheMu.Unlock()
	return nil
}

// Sensors enumerates CIM_NumericSensor (may be empty on some SKUs).
func (a *Adapter) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	a.cacheMu.Lock()
	if a.sensorCache != nil && time.Since(a.sensorCache.at) < 8*time.Second {
		out := append([]bmc.Sensor(nil), a.sensorCache.sensors...)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	data, err := a.ws.enumeratePull(ctx, uriCIMNumericSens)
	if err != nil {
		// Unsupported / empty class → empty list, not a hard failure.
		if isUnsupportedClass(err) {
			a.cacheMu.Lock()
			a.sensorCache = &cachedSensors{at: time.Now(), sensors: []bmc.Sensor{}}
			a.cacheMu.Unlock()
			return []bmc.Sensor{}, nil
		}
		return nil, err
	}
	items := eachNamedElement(data, "CIM_NumericSensor")
	out := make([]bmc.Sensor, 0, len(items))
	for i, it := range items {
		name := it["ElementName"]
		if name == "" {
			name = it["Name"]
		}
		if name == "" {
			name = fmt.Sprintf("sensor-%d", i)
		}
		val := it["CurrentReading"]
		unit := it["BaseUnits"]
		status := it["OperationalStatus"]
		if status == "" {
			status = "ok"
		}
		present := val != ""
		out = append(out, bmc.Sensor{
			ID:      fmt.Sprintf("%d", i),
			Name:    name,
			Type:    it["SensorType"],
			Value:   val,
			Unit:    unit,
			Status:  status,
			Present: present,
		})
	}

	a.cacheMu.Lock()
	a.sensorCache = &cachedSensors{at: time.Now(), sensors: out}
	a.cacheMu.Unlock()
	return append([]bmc.Sensor(nil), out...), nil
}

// SEL pulls AMT_EventLogEntry when available.
func (a *Adapter) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	a.cacheMu.Lock()
	if a.selCache != nil && time.Since(a.selCache.at) < 12*time.Second {
		out := truncateSEL(a.selCache.entries, limit)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	data, err := a.ws.enumeratePull(ctx, uriAMTEventLog)
	if err != nil {
		if isUnsupportedClass(err) {
			a.cacheMu.Lock()
			a.selCache = &cachedSEL{at: time.Now(), entries: []bmc.SELEntry{}}
			a.cacheMu.Unlock()
			return []bmc.SELEntry{}, nil
		}
		return nil, err
	}
	items := eachNamedElement(data, "AMT_EventLogEntry")
	entries := make([]bmc.SELEntry, 0, len(items))
	for i, it := range items {
		desc := it["Description"]
		if desc == "" {
			desc = it["EventData"]
		}
		if desc == "" {
			desc = it["Message"]
		}
		ts := parseAMTTime(it["CreationTimeStamp"])
		if ts.IsZero() {
			ts = parseAMTTime(it["TimeStamp"])
		}
		entries = append(entries, bmc.SELEntry{
			ID:          fmt.Sprintf("%d", i),
			Timestamp:   ts,
			SensorType:  it["EventSensorType"],
			SensorName:  it["DeviceAddress"],
			Description: desc,
			Severity:    it["EventType"],
		})
	}
	// Newest first when timestamps exist.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	a.cacheMu.Lock()
	a.selCache = &cachedSEL{at: time.Now(), entries: entries}
	a.cacheMu.Unlock()
	return truncateSEL(entries, limit), nil
}

func truncateSEL(in []bmc.SELEntry, limit int) []bmc.SELEntry {
	if limit > len(in) {
		limit = len(in)
	}
	return append([]bmc.SELEntry(nil), in[:limit]...)
}

func parseAMTTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	// CIM datetime: yyyymmddhhmmss.mmmmmm+utc
	if len(s) >= 14 && s[0] >= '0' && s[0] <= '9' {
		t, err := time.Parse("20060102150405", s[:14])
		if err == nil {
			return t.UTC()
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func isUnsupportedClass(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "fault") ||
		strings.Contains(msg, "destination unreachable") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "http 400") ||
		strings.Contains(msg, "http 500")
}

var (
	_ bmc.Client       = (*Adapter)(nil)
	_ bmc.Capabilities = (*Adapter)(nil)
)
