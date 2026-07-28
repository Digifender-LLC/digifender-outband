package ipmi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"outband/internal/bmc"
)

func TestNewFeatures(t *testing.T) {
	a := New(Config{})
	got := a.Features()
	want := bmc.AllIPMIFeatures()
	if got != want {
		t.Fatalf("Features()=%#x want %#x", got, want)
	}
	if got.Has(bmc.FeatureKVM) {
		t.Fatal("Features must not include FeatureKVM")
	}
}

func TestNewDefaultPort(t *testing.T) {
	a := New(Config{})
	if a.cfg.Port != 623 {
		t.Fatalf("Port=%d want 623", a.cfg.Port)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

func TestSessionBroken(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain command", err: fmt.Errorf("sensor foo"), want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
		{name: "wrapped cancel", err: fmt.Errorf("op: %w", context.Canceled), want: false},
		{name: "EOF", err: io.EOF, want: true},
		{name: "wrapped EOF", err: fmt.Errorf("read: %w", io.EOF), want: true},
		{name: "timeout", err: timeoutError{}, want: true},
		{name: "ECONNRESET", err: syscall.ECONNRESET, want: true},
		{name: "EPIPE", err: syscall.EPIPE, want: true},
		{name: "session string", err: errors.New("IPMI session closed"), want: true},
		{name: "not connected", err: errors.New("client not connected"), want: true},
		{name: "broken pipe string", err: errors.New("write: broken pipe"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionBroken(tt.err); got != tt.want {
				t.Fatalf("sessionBroken(%v)=%v want %v", tt.err, got, tt.want)
			}
		})
	}
}
