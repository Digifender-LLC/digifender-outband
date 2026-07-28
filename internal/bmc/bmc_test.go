package bmc

import (
	"context"
	"testing"
)

func TestFeatureSetHas(t *testing.T) {
	s := FeatureSet(FeaturePower | FeatureConsole)
	if !s.Has(FeaturePower) {
		t.Fatal("expected FeaturePower")
	}
	if !s.Has(FeatureConsole) {
		t.Fatal("expected FeatureConsole")
	}
	if s.Has(FeatureKVM) {
		t.Fatal("did not expect FeatureKVM")
	}
	if s.Has(FeatureSensors) {
		t.Fatal("did not expect FeatureSensors")
	}
}

func TestAllIPMIFeatures(t *testing.T) {
	s := AllIPMIFeatures()
	want := []Feature{
		FeaturePower, FeatureSensors, FeatureSEL,
		FeatureConsole, FeatureIdentity,
	}
	for _, f := range want {
		if !s.Has(f) {
			t.Fatalf("AllIPMIFeatures missing %d", f)
		}
	}
	if s.Has(FeatureKVM) {
		t.Fatal("AllIPMIFeatures must not include FeatureKVM")
	}
}

type stubCaps struct {
	features FeatureSet
}

func (s stubCaps) Features() FeatureSet { return s.features }

func (stubCaps) MCInfo(context.Context) (*MCInfo, error)           { return nil, nil }
func (stubCaps) PowerStatus(context.Context) (*PowerStatus, error) { return nil, nil }
func (stubCaps) PowerControl(context.Context, PowerAction) error   { return nil }
func (stubCaps) Sensors(context.Context) ([]Sensor, error)         { return nil, nil }
func (stubCaps) SEL(context.Context, int) ([]SELEntry, error)      { return nil, nil }
func (stubCaps) Close() error                                      { return nil }

type stubClient struct{}

func (stubClient) MCInfo(context.Context) (*MCInfo, error)           { return nil, nil }
func (stubClient) PowerStatus(context.Context) (*PowerStatus, error) { return nil, nil }
func (stubClient) PowerControl(context.Context, PowerAction) error   { return nil }
func (stubClient) Sensors(context.Context) ([]Sensor, error)         { return nil, nil }
func (stubClient) SEL(context.Context, int) ([]SELEntry, error)      { return nil, nil }
func (stubClient) Close() error                                      { return nil }

type stubConsole struct {
	stubClient
}

func (stubConsole) OpenSOL(context.Context) (SOLSession, error) { return nil, ErrUnsupported }

func TestClientFeatures(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := ClientFeatures(nil); got != 0 {
			t.Fatalf("got %#x want 0", got)
		}
	})
	t.Run("with Capabilities", func(t *testing.T) {
		c := stubCaps{features: FeatureSet(FeaturePower | FeatureIdentity)}
		got := ClientFeatures(c)
		if !got.Has(FeaturePower) || !got.Has(FeatureIdentity) {
			t.Fatalf("got %#x", got)
		}
		if got.Has(FeatureKVM) || got.Has(FeatureConsole) {
			t.Fatalf("unexpected bits %#x", got)
		}
	})
	t.Run("without Capabilities", func(t *testing.T) {
		got := ClientFeatures(stubClient{})
		for _, f := range []Feature{FeaturePower, FeatureSensors, FeatureSEL, FeatureConsole, FeatureIdentity} {
			if !got.Has(f) {
				t.Fatalf("legacy default missing %d", f)
			}
		}
		if got.Has(FeatureKVM) {
			t.Fatal("legacy default must not include FeatureKVM")
		}
	})
}

func TestAsConsole(t *testing.T) {
	if _, ok := AsConsole(stubClient{}); ok {
		t.Fatal("Client-only stub must not satisfy Console")
	}
	if _, ok := AsConsole(stubConsole{}); !ok {
		t.Fatal("stubConsole must satisfy Console")
	}
}
