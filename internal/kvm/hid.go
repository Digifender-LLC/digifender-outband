package kvm

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"outband/internal/rfb"
)

// HID report wire format (clean-room port of Tyan JViewer USBKeyboardRep /
// USBMouseRep). Each report is a full IVTP type=6 (HID_PKT) packet:
//
//	[0:7]     IVTP header   type=6, size=payloadLen, status=0
//	[7:15]    signature     "IUSB    "
//	[15]      major=1  [16] minor=0  [17] IUSB hdr size=32  [18] checksum
//	[19:23]   field         = USB report data length code (9 kbd / 7 mouse-abs)
//	[23]      0
//	[24]      device type   = 48 keyboard / 49 mouse
//	[25]      protocol      = 16 keyboard data / 32 mouse data
//	[26]      direction     = 0x80 (IUSB_FROM_REMOTE)
//	[27]      devnum=2  [28] ifnum (0 kbd / 1 mouse)  [29:31] reserved
//	[31:35]   seq           = SeqNum (incrementing)
//	[35:39]   reserved
//	[39]      data length   = 8 keyboard / 6 mouse-abs
//	[40:..]   USB report
//
// Checksum at [18] = -(sum(buf[7:39]) & 0xFF) over the 32-byte IUSB header only.
// Unencrypted form only; KMCrypt RC4 is TODO when the BMC enables KM encryption.

const (
	iusbDeviceKeybd = 48
	iusbDeviceMouse = 49
	iusbProtoKeybd  = 16
	iusbProtoMouse  = 32
	iusbFromRemote  = 0x80
)

// hidSeq is a process-wide sequence counter mirroring the Java static SeqNum.
var hidSeq uint32

// iusbBase is the offset of the IUSB header (immediately after the 7-byte IVTP).
const iusbBase = HeaderSize // 7

// usbDataOff is where the USB HID report begins (after IUSB + data-length byte).
const usbDataOff = iusbBase + 33 // 40

// fillIUSBHeader writes the 32-byte IUSB header + 1-byte data length into
// buf[iusbBase:], leaving room for the USB report at usbDataOff.
func fillIUSBHeader(buf []byte, reportField uint32, device, proto, ifnum, dataLen byte) {
	b := iusbBase
	copy(buf[b:b+8], []byte{'I', 'U', 'S', 'B', ' ', ' ', ' ', ' '})
	buf[b+8] = 1  // major
	buf[b+9] = 0  // minor
	buf[b+10] = 32 // IUSB header size
	buf[b+11] = 0  // checksum placeholder
	binary.LittleEndian.PutUint32(buf[b+12:b+16], reportField)
	buf[b+16] = 0
	buf[b+17] = device
	buf[b+18] = proto
	buf[b+19] = iusbFromRemote
	buf[b+20] = 2     // devnum
	buf[b+21] = ifnum // ifnum
	buf[b+22] = 0
	buf[b+23] = 0
	binary.LittleEndian.PutUint32(buf[b+24:b+28], atomic.AddUint32(&hidSeq, 1)-1)
	buf[b+28] = 0
	buf[b+29] = 0
	buf[b+30] = 0
	buf[b+31] = 0
	buf[b+32] = dataLen
}

// finishChecksum fills the IUSB checksum byte, matching the Java loop over the
// 32-byte IUSB header (not including the data-length byte or USB report).
func finishChecksum(buf []byte) {
	b := iusbBase
	var sum int
	for i := b; i < b+32; i++ {
		sum = (sum + int(buf[i])) & 0xFF
	}
	buf[b+11] = byte(-(sum & 0xFF))
}

// keyboardReport builds a full IVTP HID keyboard packet from an 8-byte USB
// keyboard report (modifier, 0, key0..key5).
func keyboardReport(usb [8]byte) []byte {
	const payload = 41
	buf := make([]byte, HeaderSize+payload)
	copy(buf, header{Type: opHIDPkt, Size: payload, Status: 0}.marshal())
	fillIUSBHeader(buf, 9, iusbDeviceKeybd, iusbProtoKeybd, 0, 8)
	copy(buf[usbDataOff:usbDataOff+8], usb[:])
	finishChecksum(buf)
	return buf
}

// mouseAbsReport builds a full IVTP absolute-mouse HID packet. btn is the USB
// button bitmask, x/y are 0..0x7FFF absolute coordinates, wheel is the signed
// wheel delta.
func mouseAbsReport(btn byte, x, y int16, wheel byte) []byte {
	const payload = 39
	buf := make([]byte, HeaderSize+payload)
	copy(buf, header{Type: opHIDPkt, Size: payload, Status: 0}.marshal())
	fillIUSBHeader(buf, 7, iusbDeviceMouse, iusbProtoMouse, 1, 6)
	buf[usbDataOff] = btn
	binary.LittleEndian.PutUint16(buf[usbDataOff+1:usbDataOff+3], uint16(x))
	binary.LittleEndian.PutUint16(buf[usbDataOff+3:usbDataOff+5], uint16(y))
	buf[usbDataOff+5] = wheel
	finishChecksum(buf)
	return buf
}

// SendHIDReport writes a pre-built IVTP HID packet to the BMC using the client's
// write mutex.
func (c *Client) SendHIDReport(report []byte) error { return c.write(report) }

// HIDSink implements rfb.Sink, maintaining keyboard and mouse state and emitting
// USB HID reports to the BMC via the Client.
type HIDSink struct {
	c   *Client
	ctx context.Context // cancels an in-flight paste when the session ends

	fbW, fbH int // current framebuffer size, for absolute-mouse scaling
	szMu     sync.RWMutex

	mu      sync.Mutex
	mods    byte   // current modifier bitmask (byte0)
	pressed []byte // up to 6 currently-held USB usage codes
}

// NewSink returns a HID Sink that drives keyboard/mouse over the given Client.
// It defaults to absolute mouse mode; coordinates are scaled against the frame
// size supplied via SetFrameSize. ctx bounds asynchronous work (a CutText paste)
// to the session lifetime. The concrete type is returned so callers can update
// the frame size on resolution change; it satisfies rfb.Sink.
func NewSink(ctx context.Context, c *Client, fbW, fbH int) *HIDSink {
	if fbW <= 0 || fbH <= 0 {
		fbW, fbH = 1024, 768
	}
	return &HIDSink{ctx: ctx, c: c, fbW: fbW, fbH: fbH, pressed: make([]byte, 0, 6)}
}

// SetFrameSize updates the framebuffer dimensions used to scale absolute mouse
// coordinates. Call this when the BMC resolution changes.
func (s *HIDSink) SetFrameSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	s.szMu.Lock()
	s.fbW, s.fbH = w, h
	s.szMu.Unlock()
}

// KeyEvent implements rfb.Sink. It maps the keysym to a modifier bit or USB
// usage code, updates held state, and sends the resulting 8-byte report.
func (s *HIDSink) KeyEvent(keysym uint32, down bool) {
	s.mu.Lock()
	if usage, ok := usbUsageFromKeysym(keysym); ok {
		// Scancode pass-through (international layouts): the keysym carries a raw
		// USB usage. Route modifier usages to the byte0 bitmask and everything
		// else to a key slot, exactly as the US-layout path below does.
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
		return // unmapped key: nothing to send
	}
	report := s.buildKeyboardReport()
	s.mu.Unlock()
	_ = s.c.SendHIDReport(report)
}

// addKey adds usage to the pressed set (max 6) if not already present. Caller
// holds s.mu.
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

// removeKey drops usage from the pressed set. Caller holds s.mu.
func (s *HIDSink) removeKey(usage byte) {
	for i, u := range s.pressed {
		if u == usage {
			s.pressed = append(s.pressed[:i], s.pressed[i+1:]...)
			return
		}
	}
}

// buildKeyboardReport assembles the 8-byte USB report and wraps it. Caller holds
// s.mu.
func (s *HIDSink) buildKeyboardReport() []byte {
	var usb [8]byte
	usb[0] = s.mods
	usb[1] = 0
	for i, u := range s.pressed {
		if i >= 6 {
			break
		}
		usb[2+i] = u
	}
	return keyboardReport(usb)
}

// PointerEvent implements rfb.Sink. It maps the RFB button mask to USB buttons,
// scales x/y into the 0..0x7FFF absolute range, derives a wheel delta from the
// scroll bits, and sends an absolute-mouse report.
func (s *HIDSink) PointerEvent(x, y int, buttons uint8) {
	s.szMu.RLock()
	w, h := s.fbW, s.fbH
	s.szMu.RUnlock()

	ax, ay := scaleAbs(x, y, w, h)
	btn := rfbButtonsToUSB(buttons)
	wheel := rfbWheel(buttons)

	_ = s.c.SendHIDReport(mouseAbsReport(btn, ax, ay, wheel))
}

// pasteKeyInterval paces synthetic keystrokes during a CutText paste. The guest
// USB stack needs a distinct key-up between key-downs to register repeats, and
// sending reports back-to-back at full speed can overrun it; ~8ms/event is
// imperceptible for typical pastes yet reliable.
const pasteKeyInterval = 8 * time.Millisecond

// CutText implements rfb.Sink by "pasting as keystrokes": each character of the
// clipboard text is typed as a press+release of the corresponding USB HID key.
// There is no shared clipboard with a KVM target, so this is the only way to get
// text in. Runs asynchronously so the RFB read loop is not blocked for the whole
// paste. Non-ASCII characters (e.g. Cyrillic) have no fixed scancode and are
// skipped — they depend on the guest keyboard layout (see docs).
func (s *HIDSink) CutText(text string) {
	if text == "" {
		return
	}
	go func() {
		for _, r := range text {
			usage, shift, ok := asciiToUsage(r)
			if !ok {
				continue
			}
			var down [8]byte
			if shift {
				down[0] = modLeftShift
			}
			down[2] = usage
			_ = s.c.SendHIDReport(keyboardReport(down))
			if !s.pasteDelay() {
				return
			}
			var up [8]byte // all-zero report = release everything
			_ = s.c.SendHIDReport(keyboardReport(up))
			if !s.pasteDelay() {
				return
			}
		}
	}()
}

// pasteDelay waits one paste interval, returning false if the session context is
// cancelled meanwhile so the paste goroutine stops instead of typing into a torn-
// down connection for the rest of a long clipboard.
func (s *HIDSink) pasteDelay() bool {
	if s.ctx == nil {
		time.Sleep(pasteKeyInterval)
		return true
	}
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(pasteKeyInterval):
		return true
	}
}

// shiftedASCII is the set of printable ASCII characters that require Shift to be
// held (besides the A–Z letters, handled separately).
var shiftedASCII = map[rune]bool{
	'!': true, '@': true, '#': true, '$': true, '%': true, '^': true,
	'&': true, '*': true, '(': true, ')': true, '_': true, '+': true,
	'{': true, '}': true, '|': true, ':': true, '"': true, '~': true,
	'<': true, '>': true, '?': true,
}

// asciiToUsage maps a rune to its USB HID usage code and whether Shift must be
// held. ok is false for characters with no fixed US-layout scancode (control
// chars other than Tab/Enter, and any non-ASCII rune such as Cyrillic).
func asciiToUsage(r rune) (usage byte, shift bool, ok bool) {
	switch r {
	case '\n', '\r':
		return 0x28, false, true // Enter
	case '\t':
		return 0x2b, false, true // Tab
	}
	if r < 0x20 || r > 0x7e {
		return 0, false, false
	}
	u := usbUsage[uint32(r)]
	if u == 0 {
		return 0, false, false
	}
	shift = (r >= 'A' && r <= 'Z') || shiftedASCII[r]
	return u, shift, true
}

// rfbButtonsToUSB maps the RFB pointer button mask to the USB HID mouse button
// bitmask. RFB: bit0 left, bit1 middle, bit2 right. USB: bit0 left, bit1 right,
// bit2 middle (the middle/right bits are swapped). Wheel bits (3/4) are not
// buttons and are excluded here.
func rfbButtonsToUSB(buttons uint8) byte {
	var btn byte
	if buttons&0x01 != 0 {
		btn |= 0x01 // left
	}
	if buttons&0x04 != 0 {
		btn |= 0x02 // right
	}
	if buttons&0x02 != 0 {
		btn |= 0x04 // middle
	}
	return btn
}

// rfbWheel derives the signed USB wheel delta byte from the RFB scroll bits
// (bit3 wheel-up → +1, bit4 wheel-down → -1).
func rfbWheel(buttons uint8) byte {
	if buttons&0x08 != 0 {
		return 1
	}
	if buttons&0x10 != 0 {
		return 0xff // -1
	}
	return 0
}

// scaleAbs maps framebuffer pixel coordinates into the BMC's absolute 0..0x7FFF
// range (DIRABS_MAX_SCALED in USBMouseRep), matching the Java rounding
// (x*32767/w + 0.5).
func scaleAbs(x, y, w, h int) (int16, int16) {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	clamp := func(v, max int) int {
		if v < 0 {
			return 0
		}
		if v > max {
			return max
		}
		return v
	}
	x = clamp(x, w)
	y = clamp(y, h)
	ax := (x*32767 + w/2) / w
	ay := (y*32767 + h/2) / h
	return int16(ax), int16(ay)
}

// compile-time assertion that HIDSink satisfies rfb.Sink.
var _ rfb.Sink = (*HIDSink)(nil)
