package redir

import (
	"sync/atomic"

	"outband/internal/rfb"
)

// lateSink is an rfb.Sink whose backing sink is published asynchronously once
// the BMC session is up (mirrors internal/kvm).
type lateSink struct {
	v atomic.Pointer[rfb.Sink]
}

func newLateSink() *lateSink { return &lateSink{} }

func (l *lateSink) set(s rfb.Sink) { l.v.Store(&s) }

func (l *lateSink) KeyEvent(keysym uint32, down bool) {
	if p := l.v.Load(); p != nil {
		(*p).KeyEvent(keysym, down)
	}
}

func (l *lateSink) PointerEvent(x, y int, buttons uint8) {
	if p := l.v.Load(); p != nil {
		(*p).PointerEvent(x, y, buttons)
	}
}

func (l *lateSink) CutText(text string) {
	if p := l.v.Load(); p != nil {
		(*p).CutText(text)
	}
}

var _ rfb.Sink = (*lateSink)(nil)
