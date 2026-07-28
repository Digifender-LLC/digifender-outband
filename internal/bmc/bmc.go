// Package bmc defines the BMC control-plane interface used by mIPMI.
package bmc

import (
	"context"
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
)

// FeatureSet is a bitmask of supported features.
type FeatureSet Feature

// Has reports whether f is included.
func (s FeatureSet) Has(f Feature) bool {
	return Feature(s)&f != 0
}

// AllIPMIFeatures is the feature set exposed by the IPMI adapter.
func AllIPMIFeatures() FeatureSet {
	return FeatureSet(FeaturePower | FeatureSensors | FeatureSEL | FeatureConsole | FeatureIdentity)
}

// Capabilities is an optional interface providers may implement.
type Capabilities interface {
	Features() FeatureSet
}

// MCInfo is identity/firmware information from the BMC.
type MCInfo struct {
	DeviceID        uint8
	FirmwareRev     string
	ProtocolVersion string // e.g. IPMI "2.0", Redfish revision
	ManufacturerID  uint32
	Manufacturer    string
	ProductID       uint16
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
	ID      uint8
	Name    string
	Type    string
	Value   string
	Unit    string
	Status  string
	Present bool
}

// SELEntry is a system event log record.
type SELEntry struct {
	ID          uint16
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
type Client interface {
	MCInfo(ctx context.Context) (*MCInfo, error)
	PowerStatus(ctx context.Context) (*PowerStatus, error)
	PowerControl(ctx context.Context, action PowerAction) error
	Sensors(ctx context.Context) ([]Sensor, error)
	SEL(ctx context.Context, limit int) ([]SELEntry, error)
	OpenSOL(ctx context.Context) (SOLSession, error)
	Close() error
}
