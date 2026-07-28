package ilo

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"outband/internal/bmc"
)

// Config is the Redfish connection settings for HPE iLO.
type Config struct {
	Host               string
	Port               int // 0 → 443
	User               string
	Password           string
	InsecureSkipVerify bool
}

// Adapter implements bmc.Client over iLO Redfish (HTTPS Basic auth).
type Adapter struct {
	cfg Config
	rf  *redfishClient

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

// New creates an iLO adapter. Connection is established lazily on first call.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, rf: newRedfish(cfg)}
}

// Features reports the iLO adapter capability set (no console/KVM in v1).
func (a *Adapter) Features() bmc.FeatureSet {
	return bmc.FeatureSet(bmc.FeaturePower | bmc.FeatureSensors | bmc.FeatureSEL | bmc.FeatureIdentity)
}

// Close releases idle HTTP connections.
func (a *Adapter) Close() error {
	if a.rf != nil && a.rf.http != nil {
		a.rf.http.CloseIdleConnections()
	}
	return nil
}

// MCInfo returns iLO/system identity from Redfish ServiceRoot, Managers, and Systems.
func (a *Adapter) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	a.cacheMu.Lock()
	if a.mcInfoCache != nil && time.Since(a.mcInfoCache.at) < 15*time.Second {
		info := *a.mcInfoCache.info
		a.cacheMu.Unlock()
		return &info, nil
	}
	a.cacheMu.Unlock()

	var root struct {
		RedfishVersion string `json:"RedfishVersion"`
		Oem            struct {
			Hp struct {
				Manager []struct {
					ManagerType            string `json:"ManagerType"`
					ManagerFirmwareVersion string `json:"ManagerFirmwareVersion"`
				} `json:"Manager"`
			} `json:"Hp"`
		} `json:"Oem"`
	}
	var mgr struct {
		FirmwareVersion string `json:"FirmwareVersion"`
		Firmware        struct {
			Current struct {
				VersionString string `json:"VersionString"`
			} `json:"Current"`
		} `json:"Firmware"`
	}
	var sys struct {
		Manufacturer string `json:"Manufacturer"`
		Model        string `json:"Model"`
	}

	info := &bmc.MCInfo{
		Manufacturer:    "HPE",
		Model:           "iLO",
		ProtocolVersion: "Redfish",
		FirmwareRev:     "unknown",
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/", &root); err == nil {
		if root.RedfishVersion != "" {
			info.ProtocolVersion = root.RedfishVersion
		}
		if len(root.Oem.Hp.Manager) > 0 {
			m := root.Oem.Hp.Manager[0]
			if m.ManagerType != "" {
				info.Model = m.ManagerType
			}
			if m.ManagerFirmwareVersion != "" {
				info.FirmwareRev = m.ManagerType + " v" + m.ManagerFirmwareVersion
				if m.ManagerType == "" {
					info.FirmwareRev = m.ManagerFirmwareVersion
				}
			}
		}
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Managers/1/", &mgr); err == nil {
		fw := mgr.FirmwareVersion
		if fw == "" {
			fw = mgr.Firmware.Current.VersionString
		}
		if fw != "" {
			info.FirmwareRev = fw
		}
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Systems/1/", &sys); err == nil {
		if sys.Manufacturer != "" {
			info.Manufacturer = sys.Manufacturer
		}
		if sys.Model != "" {
			info.Model = sys.Model
		}
	}
	if info.Model == "" {
		info.Model = "iLO"
	}

	a.cacheMu.Lock()
	a.mcInfoCache = &cachedMCInfo{at: time.Now(), info: info}
	a.cacheMu.Unlock()
	cp := *info
	return &cp, nil
}

// PowerStatus reads ComputerSystem.PowerState.
func (a *Adapter) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	a.cacheMu.Lock()
	if a.powerCache != nil && time.Since(a.powerCache.at) < 3*time.Second {
		st := *a.powerCache.status
		a.cacheMu.Unlock()
		return &st, nil
	}
	a.cacheMu.Unlock()

	var sys struct {
		PowerState string `json:"PowerState"`
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Systems/1/", &sys); err != nil {
		return nil, err
	}
	status := &bmc.PowerStatus{IsOn: strings.EqualFold(sys.PowerState, "On")}

	a.cacheMu.Lock()
	a.powerCache = &cachedPower{at: time.Now(), status: status}
	a.cacheMu.Unlock()
	cp := *status
	return &cp, nil
}

// PowerControl posts ComputerSystem.Reset.
func (a *Adapter) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	var resetType string
	switch action {
	case bmc.PowerOn:
		resetType = "On"
	case bmc.PowerOff:
		resetType = "ForceOff"
	case bmc.PowerCycle:
		resetType = "ForceRestart"
	case bmc.PowerSoft:
		resetType = "PushPowerButton"
	default:
		return fmt.Errorf("%w: power action %q", bmc.ErrUnsupported, action)
	}
	body := map[string]string{"ResetType": resetType}
	if err := a.rf.postJSON(ctx, "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/", body); err != nil {
		return err
	}
	a.cacheMu.Lock()
	a.powerCache = nil
	a.cacheMu.Unlock()
	return nil
}

// Sensors reads Chassis Thermal when available; otherwise synthesizes health/power sensors.
func (a *Adapter) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	a.cacheMu.Lock()
	if a.sensorCache != nil && time.Since(a.sensorCache.at) < 8*time.Second {
		out := append([]bmc.Sensor(nil), a.sensorCache.sensors...)
		a.cacheMu.Unlock()
		return out, nil
	}
	a.cacheMu.Unlock()

	out, err := a.readThermalSensors(ctx)
	if err != nil || len(out) == 0 {
		out = a.syntheticSensors(ctx)
	}

	a.cacheMu.Lock()
	a.sensorCache = &cachedSensors{at: time.Now(), sensors: out}
	a.cacheMu.Unlock()
	return append([]bmc.Sensor(nil), out...), nil
}

func (a *Adapter) readThermalSensors(ctx context.Context) ([]bmc.Sensor, error) {
	var thermal struct {
		Temperatures []struct {
			Name           string    `json:"Name"`
			MemberID       string    `json:"MemberId"`
			ReadingCelsius float64   `json:"ReadingCelsius"`
			Status         statusObj `json:"Status"`
		} `json:"Temperatures"`
		Fans []struct {
			Name         string    `json:"Name"`
			MemberID     string    `json:"MemberId"`
			Reading      float64   `json:"Reading"`
			ReadingUnits string    `json:"ReadingUnits"`
			Status       statusObj `json:"Status"`
		} `json:"Fans"`
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Chassis/1/Thermal/", &thermal); err != nil {
		return nil, err
	}
	out := make([]bmc.Sensor, 0, len(thermal.Temperatures)+len(thermal.Fans))
	for i, t := range thermal.Temperatures {
		name := t.Name
		if name == "" {
			name = fmt.Sprintf("Temp-%d", i)
		}
		id := t.MemberID
		if id == "" {
			id = fmt.Sprintf("temp-%d", i)
		}
		out = append(out, bmc.Sensor{
			ID:      id,
			Name:    name,
			Type:    "Temperature",
			Value:   formatFloat(t.ReadingCelsius),
			Unit:    "C",
			Status:  healthStatus(t.Status),
			Present: true,
		})
	}
	for i, f := range thermal.Fans {
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("Fan-%d", i)
		}
		id := f.MemberID
		if id == "" {
			id = fmt.Sprintf("fan-%d", i)
		}
		unit := f.ReadingUnits
		if unit == "" {
			unit = "RPM"
		}
		out = append(out, bmc.Sensor{
			ID:      id,
			Name:    name,
			Type:    "Fan",
			Value:   formatFloat(f.Reading),
			Unit:    unit,
			Status:  healthStatus(f.Status),
			Present: true,
		})
	}
	return out, nil
}

func (a *Adapter) syntheticSensors(ctx context.Context) []bmc.Sensor {
	out := make([]bmc.Sensor, 0, 2)
	var sys struct {
		Status statusObj `json:"Status"`
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Systems/1/", &sys); err == nil {
		h := healthStatus(sys.Status)
		out = append(out, bmc.Sensor{
			ID:      "system-health",
			Name:    "System Health",
			Type:    "Health",
			Value:   h,
			Unit:    "",
			Status:  h,
			Present: true,
		})
	}
	var power struct {
		PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
		PowerControl       []struct {
			PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
		} `json:"PowerControl"`
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Chassis/1/Power/", &power); err == nil {
		watts := power.PowerConsumedWatts
		if watts == 0 && len(power.PowerControl) > 0 {
			watts = power.PowerControl[0].PowerConsumedWatts
		}
		out = append(out, bmc.Sensor{
			ID:      "power-consumed",
			Name:    "Power Consumed",
			Type:    "Power",
			Value:   formatFloat(watts),
			Unit:    "W",
			Status:  "ok",
			Present: true,
		})
	}
	return out
}

type statusObj struct {
	Health string `json:"Health"`
	State  string `json:"State"`
}

func healthStatus(s statusObj) string {
	if s.Health != "" {
		return s.Health
	}
	if s.State != "" {
		return s.State
	}
	return "ok"
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// SEL reads the iLO Event Log (IEL).
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

	var coll struct {
		Items   []logEntry `json:"Items"`
		Members []logEntry `json:"Members"`
	}
	if err := a.rf.getJSON(ctx, "/redfish/v1/Managers/1/LogServices/IEL/Entries/", &coll); err != nil {
		return nil, err
	}
	raw := coll.Items
	if len(raw) == 0 {
		raw = coll.Members
	}
	// Drop stub Members that only carry @odata.id (no Message).
	entries := make([]bmc.SELEntry, 0, len(raw))
	for _, it := range raw {
		if it.Message == "" && it.ID == "" && it.RecordID == 0 {
			continue
		}
		id := it.ID
		if id == "" && it.RecordID != 0 {
			id = strconv.Itoa(it.RecordID)
		}
		if id == "" && it.Number != 0 {
			id = strconv.Itoa(it.Number)
		}
		ts := parseRedfishTime(it.Created)
		if ts.IsZero() {
			ts = parseRedfishTime(it.EventTimestamp)
		}
		sensorType := it.OemRecordFormat
		if sensorType == "" {
			sensorType = it.EntryType
		}
		entries = append(entries, bmc.SELEntry{
			ID:          id,
			Timestamp:   ts,
			SensorType:  sensorType,
			SensorName:  it.Name,
			Description: it.Message,
			Severity:    it.Severity,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ni, _ := strconv.Atoi(entries[i].ID)
		nj, _ := strconv.Atoi(entries[j].ID)
		if ni != 0 || nj != 0 {
			return ni > nj
		}
		return entries[i].ID > entries[j].ID
	})

	a.cacheMu.Lock()
	a.selCache = &cachedSEL{at: time.Now(), entries: entries}
	a.cacheMu.Unlock()
	return truncateSEL(entries, limit), nil
}

type logEntry struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	Message         string `json:"Message"`
	Severity        string `json:"Severity"`
	EntryType       string `json:"EntryType"`
	OemRecordFormat string `json:"OemRecordFormat"`
	Created         string `json:"Created"`
	EventTimestamp  string `json:"EventTimestamp"`
	RecordID        int    `json:"RecordId"`
	Number          int    `json:"Number"`
}

func truncateSEL(in []bmc.SELEntry, limit int) []bmc.SELEntry {
	if limit > len(in) {
		limit = len(in)
	}
	return append([]bmc.SELEntry(nil), in[:limit]...)
}

func parseRedfishTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

var (
	_ bmc.Client       = (*Adapter)(nil)
	_ bmc.Capabilities = (*Adapter)(nil)
)
