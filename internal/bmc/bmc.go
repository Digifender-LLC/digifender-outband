// Package bmc defines the BMC control-plane interface used by Outband.
package bmc

import (
	"context"
	"errors"
	"io"
	"time"
)

// PowerAction is a chassis power control operation.
type PowerAction string

const (
	PowerOn    PowerAction = "on"
	PowerOff   PowerAction = "off"
	PowerCycle PowerAction = "cycle"
	PowerSoft  PowerAction = "soft"
)

// Feature flags describe optional BMC capabilities.
type Feature uint64

const (
	FeaturePower Feature = 1 << iota
	FeatureSensors
	FeatureSEL
	FeatureConsole // serial / SOL-style console
	FeatureIdentity
	FeatureKVM // video KVM (e.g. AMI Adviser/IVTP)
)

// FeatureSet is a bitmask of supported features.
type FeatureSet Feature

// Has reports whether f is included.
func (s FeatureSet) Has(f Feature) bool {
	return Feature(s)&f != 0
}

// controlPlaneFeatures is the legacy default when a Client does not implement Capabilities.
// KVM is omitted because it is vendor-specific.
func controlPlaneFeatures() FeatureSet {
	return FeatureSet(FeaturePower | FeatureSensors | FeatureSEL | FeatureConsole | FeatureIdentity)
}

// AllIPMIFeatures is the feature set exposed by the IPMI adapter.
// Video KVM is not included; host inventory KVM config drives FeatureKVM in the UI.
func AllIPMIFeatures() FeatureSet {
	return controlPlaneFeatures()
}

// ErrUnsupported indicates the BMC/provider does not support the requested operation.
var ErrUnsupported = errors.New("unsupported")

// ErrBusy indicates a resource (e.g. SOL session) is already in use.
var ErrBusy = errors.New("busy")

// Capabilities is an optional interface providers may implement.
type Capabilities interface {
	Features() FeatureSet
}

// Console is an optional interface for Serial-over-LAN (or equivalent) console access.
// Advertise support with FeatureConsole; obtain via AsConsole.
type Console interface {
	OpenSOL(ctx context.Context) (SOLSession, error)
}

// AsConsole returns the Console implementation if c supports it.
func AsConsole(c Client) (Console, bool) {
	con, ok := c.(Console)
	return con, ok
}

// ClientFeatures returns the feature set for c. If c implements Capabilities, that
// set is used; otherwise the control-plane defaults without KVM are assumed.
func ClientFeatures(c Client) FeatureSet {
	if c == nil {
		return 0
	}
	if cap, ok := c.(Capabilities); ok {
		return cap.Features()
	}
	return controlPlaneFeatures()
}

// MCInfo is identity/firmware information from the BMC.
type MCInfo struct {
	FirmwareRev     string
	ProtocolVersion string // e.g. IPMI "2.0", Redfish revision
	Manufacturer    string
	Model           string // product / model name (IPMI may leave empty)
	// Optional vendor-native IDs (IPMI Get Device ID); zero means unset.
	DeviceID       uint8
	ManufacturerID uint32
	ProductID      uint16
}

// PowerStatus is chassis power state.
type PowerStatus struct {
	IsOn               bool
	PowerRestorePolicy string
	PowerFault         bool
	ChassisIntrusion   bool
}

// Sensor is a normalized sensor reading for the UI.
type Sensor struct {
	ID      string // opaque (IPMI: "%02x" of sensor number)
	Name    string
	Type    string
	Value   string
	Unit    string
	Status  string
	Present bool
}

// SELEntry is a system event log record.
type SELEntry struct {
	ID          string // opaque (IPMI: "%04x" of record id)
	Timestamp   time.Time
	SensorType  string
	SensorName  string
	Description string
	Direction   string
	Severity    string
}

// SOLSession is a bidirectional byte pipe to an active Serial-over-LAN session.
type SOLSession interface {
	io.ReadWriteCloser
}

// Client is the BMC control plane used by HTTP handlers.
// Serial console is optional; see Console and FeatureConsole.
type Client interface {
	MCInfo(ctx context.Context) (*MCInfo, error)
	PowerStatus(ctx context.Context) (*PowerStatus, error)
	PowerControl(ctx context.Context, action PowerAction) error
	Sensors(ctx context.Context) ([]Sensor, error)
	SEL(ctx context.Context, limit int) ([]SELEntry, error)
	Close() error
}
