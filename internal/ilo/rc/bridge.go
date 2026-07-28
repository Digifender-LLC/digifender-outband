package rc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"outband/internal/rfb"
)

// Bridge owns at most one iLO IRC session and exposes RFB source/sink for noVNC.
type Bridge struct {
	mu sync.Mutex

	host      string
	httpsPort int
	user      string
	pass      string
	insecure  bool
	log       *slog.Logger

	active bool
	cancel context.CancelFunc
	client *Client
}

// NewBridge prepares a single-session iLO remote-console bridge.
func NewBridge(host string, httpsPort int, user, pass string, insecure bool, log *slog.Logger) *Bridge {
	if httpsPort == 0 {
		httpsPort = 443
	}
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{
		host:      host,
		httpsPort: httpsPort,
		user:      user,
		pass:      pass,
		insecure:  insecure,
		log:       log,
	}
}

// Acquire starts the IRC session and returns RFB endpoints.
// Caller must Release (via the returned func) when the WebSocket session ends.
func (b *Bridge) Acquire(parent context.Context) (rfb.Source, rfb.Sink, func(), error) {
	b.mu.Lock()
	if b.active {
		b.mu.Unlock()
		return nil, nil, nil, ErrBusy
	}
	b.active = true
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	src := rfb.NewFrameSource(1024, 768)
	late := newLateSink()

	c, err := Connect(ctx, Options{
		Host:      b.host,
		HTTPSPort: b.httpsPort,
		User:      b.user,
		Password:  b.pass,
		Insecure:  b.insecure,
		Log:       b.log,
	})
	if err != nil {
		cancel()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		return nil, nil, nil, err
	}

	hid := NewHIDSink(ctx, c.ch, 1024, 768)
	c.FrameHook = func(w, h int, pix []byte) {
		src.Update(w, h, pix)
		hid.SetFrameSize(w, h)
	}
	late.set(hid)

	b.mu.Lock()
	b.cancel = cancel
	b.client = c
	b.mu.Unlock()

	go func() {
		err := c.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			b.log.Info("ilo rc session ended", "err", err)
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

// Release tears down the active IRC session.
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

// Status returns a short human status string.
func (b *Bridge) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active && b.client != nil {
		return fmt.Sprintf("active → %s (%s)", b.host, b.client.Status)
	}
	return fmt.Sprintf("idle (will connect to %s)", b.host)
}

// lateSink forwards to a sink set after Acquire connects.
type lateSink struct {
	mu sync.Mutex
	s  rfb.Sink
}

func newLateSink() *lateSink { return &lateSink{s: rfb.NopSink()} }

func (l *lateSink) set(s rfb.Sink) {
	l.mu.Lock()
	l.s = s
	l.mu.Unlock()
}

func (l *lateSink) KeyEvent(keysym uint32, down bool) {
	l.mu.Lock()
	s := l.s
	l.mu.Unlock()
	s.KeyEvent(keysym, down)
}

func (l *lateSink) PointerEvent(x, y int, buttons uint8) {
	l.mu.Lock()
	s := l.s
	l.mu.Unlock()
	s.PointerEvent(x, y, buttons)
}

func (l *lateSink) CutText(text string) {
	l.mu.Lock()
	s := l.s
	l.mu.Unlock()
	s.CutText(text)
}
