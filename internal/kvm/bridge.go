package kvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"outband/internal/kvm/codec"
	"outband/internal/rfb"
)

// ErrBusy is returned when another KVM session is already active.
var ErrBusy = errors.New("KVM session already active")

// Bridge owns at most one BMC KVM session and exposes RFB source/sink for noVNC.
type Bridge struct {
	mu sync.Mutex

	host string
	user string
	pass string
	port int
	tls  bool
	log  *slog.Logger

	active bool
	cancel context.CancelFunc
	client *Client
}

// NewBridge prepares a single-session KVM bridge (not yet connected).
func NewBridge(host, user, pass string, port int, useTLS bool, log *slog.Logger) *Bridge {
	if port == 0 {
		port = 7578
	}
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{
		host: host,
		user: user,
		pass: pass,
		port: port,
		tls:  useTLS,
		log:  log,
	}
}

// Acquire starts the BMC video session and returns RFB endpoints.
// Caller must Release (via the returned func) when the WebSocket session ends.
func (b *Bridge) Acquire(parent context.Context) (rfb.Source, rfb.Sink, func(), error) {
	b.mu.Lock()
	if b.active {
		b.mu.Unlock()
		return nil, nil, nil, ErrBusy
	}
	b.active = true // reserve slot before slow Connect
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	src := rfb.NewFrameSource(1024, 768)
	late := newLateSink()

	c, err := Connect(ctx, Options{
		Host: b.host,
		Port: b.port,
		TLS:  b.tls,
		User: b.user,
	}, b.pass)
	if err != nil {
		cancel()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		return nil, nil, nil, err
	}

	hid := NewSink(ctx, c, 1024, 768)
	c.OnFrame = func(f *codec.Frame) {
		src.Update(f.W, f.H, f.Pix)
		hid.SetFrameSize(f.W, f.H)
	}
	late.set(hid)

	b.mu.Lock()
	b.cancel = cancel
	b.client = c
	b.mu.Unlock()

	go func() {
		err := c.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			b.log.Info("kvm session ended", "err", err)
		}
		b.mu.Lock()
		if b.client == c {
			b.active = false
			b.client = nil
			b.cancel = nil
		}
		b.mu.Unlock()
	}()

	return src, late, b.Release, nil
}

// Release tears down the active BMC KVM session.
func (b *Bridge) Release() {
	b.mu.Lock()
	cancel := b.cancel
	c := b.client
	b.active = false
	b.cancel = nil
	b.client = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		_ = c.Close()
	}
}

// ServeRFB runs the RFB protocol on conn until done.
func ServeRFB(ctx context.Context, conn net.Conn, src rfb.Source, sink rfb.Sink) error {
	return rfb.Serve(ctx, conn, src, sink)
}

// Status returns a short human status string.
func (b *Bridge) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active {
		return fmt.Sprintf("active → %s:%d", b.host, b.port)
	}
	return fmt.Sprintf("idle (will connect to %s:%d)", b.host, b.port)
}
