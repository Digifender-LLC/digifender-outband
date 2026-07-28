package redir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"outband/internal/amt"
	"outband/internal/rfb"
)

// ErrBusy is returned when another KVM session is already active on this bridge.
var ErrBusy = errors.New("KVM session already active")

// Bridge owns at most one AMT KVM session and exposes RFB source/sink for noVNC.
type Bridge struct {
	mu sync.Mutex

	host      string
	user      string
	pass      string
	port      int
	tls       bool
	wsmanTLS  bool
	wsmanPort int
	log       *slog.Logger

	active bool
	cancel context.CancelFunc
	conn   *Conn
}

// NewBridge prepares a single-session AMT KVM bridge (not yet connected).
func NewBridge(host, user, pass string, redirPort int, redirTLS bool, wsmanPort int, wsmanTLS bool, log *slog.Logger) *Bridge {
	if redirPort == 0 {
		if redirTLS {
			redirPort = 16995
		} else {
			redirPort = 16994
		}
	}
	if wsmanPort == 0 {
		if wsmanTLS {
			wsmanPort = 16993
		} else {
			wsmanPort = 16992
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{
		host:      host,
		user:      user,
		pass:      pass,
		port:      redirPort,
		tls:       redirTLS,
		wsmanPort: wsmanPort,
		wsmanTLS:  wsmanTLS,
		log:       log,
	}
}

// Acquire enables KVM via WS-MAN if needed, dials redirection, and returns RFB endpoints.
func (b *Bridge) Acquire(parent context.Context) (rfb.Source, rfb.Sink, func(), error) {
	b.mu.Lock()
	if b.active {
		b.mu.Unlock()
		return nil, nil, nil, ErrBusy
	}
	b.active = true
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	src := rfb.NewFrameSource(800, 600)
	late := newLateSink()

	adapter := amt.New(amt.Config{
		Host:     b.host,
		Port:     b.wsmanPort,
		User:     b.user,
		Password: b.pass,
		TLS:      b.wsmanTLS,
	})
	// EnableKVM must not share the session ctx — a slow WS-MAN round-trip
	// would otherwise cancel the RFB stream via parent timeout. Failures are
	// fatal: without SAP enable, redirection dials fail opaquely.
	enCtx, enCancel := context.WithTimeout(context.Background(), 25*time.Second)
	err := adapter.EnableKVM(enCtx)
	enCancel()
	if err != nil {
		cancel()
		_ = adapter.Close()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("enable AMT Hardware-KVM: %w", err)
	}

	conn, err := Dial(Options{
		Host:     b.host,
		Port:     b.port,
		TLS:      b.tls,
		User:     b.user,
		Password: b.pass,
	})
	if err != nil {
		cancel()
		_ = adapter.Close()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		return nil, nil, nil, err
	}

	client := newRFBClient(conn)
	client.SetOnFrame(func(w, h int, pix []byte) {
		src.Update(w, h, pix)
	})
	if err := client.Handshake(); err != nil {
		_ = conn.Close()
		cancel()
		_ = adapter.Close()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("amt rfb handshake: %w", err)
	}

	sink := &hidSink{client: client}
	late.set(sink)

	b.mu.Lock()
	b.cancel = cancel
	b.conn = conn
	b.mu.Unlock()

	go func() {
		err := client.Run()
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			b.log.Info("amt kvm session ended", "err", err)
		}
		_ = adapter.Close()
		b.mu.Lock()
		if b.conn == conn {
			b.active = false
			b.conn = nil
			b.cancel = nil
		}
		b.mu.Unlock()
		_ = conn.Close()
	}()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	return src, late, b.Release, nil
}

// Release tears down the active AMT KVM session.
func (b *Bridge) Release() {
	b.mu.Lock()
	cancel := b.cancel
	c := b.conn
	b.active = false
	b.cancel = nil
	b.conn = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		_ = c.Close()
	}
}

// Status returns a short human status string.
func (b *Bridge) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active {
		return fmt.Sprintf("active → %s:%d (AMT)", b.host, b.port)
	}
	return fmt.Sprintf("idle (will connect to %s:%d AMT)", b.host, b.port)
}

type hidSink struct {
	client *rfbClient
}

func (s *hidSink) KeyEvent(keysym uint32, down bool) {
	_ = s.client.KeyEvent(keysym, down)
}

func (s *hidSink) PointerEvent(x, y int, buttons uint8) {
	_ = s.client.PointerEvent(x, y, buttons)
}

func (s *hidSink) CutText(text string) {
	for _, r := range text {
		if r > 0xff {
			continue
		}
		_ = s.client.KeyEvent(uint32(r), true)
		_ = s.client.KeyEvent(uint32(r), false)
	}
}

var _ rfb.Sink = (*hidSink)(nil)
