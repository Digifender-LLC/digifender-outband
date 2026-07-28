package rc

import (
	"context"
	"sync"
	"unicode/utf8"

	"outband/internal/rfb"
)

// HIDSink implements rfb.Sink for iLO IRC keyboard/mouse frames.
type HIDSink struct {
	ch  *Channel
	ctx context.Context

	fbW, fbH int
	szMu     sync.RWMutex

	mu      sync.Mutex
	mods    byte
	pressed []byte
}

// NewHIDSink returns a sink bound to the console channel.
func NewHIDSink(ctx context.Context, ch *Channel, fbW, fbH int) *HIDSink {
	if fbW <= 0 || fbH <= 0 {
		fbW, fbH = 1024, 768
	}
	return &HIDSink{ctx: ctx, ch: ch, fbW: fbW, fbH: fbH, pressed: make([]byte, 0, 6)}
}

// SetFrameSize updates absolute-mouse scaling dimensions.
func (s *HIDSink) SetFrameSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	s.szMu.Lock()
	s.fbW, s.fbH = w, h
	s.szMu.Unlock()
}

// KeyEvent implements rfb.Sink.
func (s *HIDSink) KeyEvent(keysym uint32, down bool) {
	s.mu.Lock()
	if usage, ok := usbUsageFromKeysym(keysym); ok {
		if bit := modBitForUsage(usage); bit != 0 {
			if down {
				s.mods |= bit
			} else {
				s.mods &^= bit
			}
		} else if down {
			s.addKey(usage)
		} else {
			s.removeKey(usage)
		}
	} else if bit := modBitFor(keysym); bit != 0 {
		if down {
			s.mods |= bit
		} else {
			s.mods &^= bit
		}
	} else if usage := usageFor(keysym); usage != 0 {
		if down {
			s.addKey(usage)
		} else {
			s.removeKey(usage)
		}
	} else {
		s.mu.Unlock()
		return
	}
	frame := KeyboardReport(s.mods, s.pressed)
	s.mu.Unlock()
	_ = s.ch.Send(frame)
}

func (s *HIDSink) addKey(usage byte) {
	for _, u := range s.pressed {
		if u == usage {
			return
		}
	}
	if len(s.pressed) < 6 {
		s.pressed = append(s.pressed, usage)
	}
}

func (s *HIDSink) removeKey(usage byte) {
	for i, u := range s.pressed {
		if u == usage {
			s.pressed = append(s.pressed[:i], s.pressed[i+1:]...)
			return
		}
	}
}

// PointerEvent implements rfb.Sink.
func (s *HIDSink) PointerEvent(x, y int, buttons uint8) {
	s.szMu.RLock()
	w, h := s.fbW, s.fbH
	s.szMu.RUnlock()
	frame := MouseAbsolute(x, y, w, h, 0, 0, RFBButtonsToILO(buttons))
	_ = s.ch.Send(frame)
}

// CutText types clipboard text as keystrokes (best-effort ASCII/latin-1).
func (s *HIDSink) CutText(text string) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		for _, r := range text {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if r > utf8.RuneSelf && r > 0xff {
				continue
			}
			ks := uint32(r)
			s.KeyEvent(ks, true)
			s.KeyEvent(ks, false)
		}
	}()
}

// Compile-time check.
var _ rfb.Sink = (*HIDSink)(nil)
