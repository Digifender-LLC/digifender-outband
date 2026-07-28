package kvm

import (
	"encoding/binary"
	"testing"
)

func TestUsageFor(t *testing.T) {
	cases := []struct {
		name   string
		keysym uint32
		want   byte
	}{
		{"a", 'a', 0x04},
		{"A", 'A', 0x04},
		{"z", 'z', 0x1d},
		{"1", '1', 0x1e},
		{"0", '0', 0x27},
		{"shifted 1 (!)", '!', 0x1e},
		{"Enter", 0xff0d, 0x28},
		{"Escape", 0xff1b, 0x29},
		{"Backspace", 0xff08, 0x2a},
		{"Tab", 0xff09, 0x2b},
		{"Space", ' ', 0x2c},
		{"Up", 0xff52, 0x52},
		{"Down", 0xff54, 0x51},
		{"Left", 0xff51, 0x50},
		{"Right", 0xff53, 0x4f},
		{"F1", 0xffbe, 0x3a},
		{"F12", 0xffc9, 0x45},
		{"minus", '-', 0x2d},
		{"slash", '/', 0x38},
		{"unmapped", 0x12345, 0x00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usageFor(c.keysym); got != c.want {
				t.Errorf("usageFor(%#x) = %#x, want %#x", c.keysym, got, c.want)
			}
		})
	}
}

func TestModBitFor(t *testing.T) {
	cases := []struct {
		name   string
		keysym uint32
		want   byte
	}{
		{"LeftShift", 0xffe1, modLeftShift},
		{"RightShift", 0xffe2, modRightShift},
		{"LeftCtrl", 0xffe3, modLeftCtrl},
		{"RightCtrl", 0xffe4, modRightCtrl},
		{"LeftAlt", 0xffe9, modLeftAlt},
		{"LeftGUI", 0xffeb, modLeftGUI},
		{"not a modifier", 'a', 0x00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := modBitFor(c.keysym); got != c.want {
				t.Errorf("modBitFor(%#x) = %#x, want %#x", c.keysym, got, c.want)
			}
		})
	}
}

func TestUsbUsageFromKeysym(t *testing.T) {
	cases := []struct {
		name   string
		keysym uint32
		want   byte
		wantOK bool
	}{
		{"passthrough a-position", scancodePassthroughBase | 0x04, 0x04, true},
		{"passthrough IntlBackslash", scancodePassthroughBase | 0x64, 0x64, true},
		{"passthrough left-ctrl usage", scancodePassthroughBase | 0xE0, 0xE0, true},
		{"ordinary ascii keysym", 'a', 0x00, false},
		{"ordinary special keysym", 0xff0d, 0x00, false},
		{"unicode keysym not misread", 0x01000400, 0x00, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := usbUsageFromKeysym(c.keysym)
			if got != c.want || ok != c.wantOK {
				t.Errorf("usbUsageFromKeysym(%#x) = (%#x,%v), want (%#x,%v)",
					c.keysym, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestScancodePassthrough drives the sink with pass-through keysyms (raw USB
// usages carried in the keysym low byte) and checks that regular keys land in a
// key slot while modifier usages set byte0 — independent of the US keysym table.
func TestScancodePassthrough(t *testing.T) {
	s := &HIDSink{pressed: make([]byte, 0, 6)}

	// A physical-Y-position key (USB usage 0x1c) — on a German layout this is the
	// 'z' key; the guest's keymap, not ours, decides the character.
	rep := s.KeyEventBuild(scancodePassthroughBase|0x1c, true)
	usb := usbFromReport(rep)
	if usb[0] != 0 {
		t.Errorf("modifier byte = %#x, want 0", usb[0])
	}
	if usb[2] != 0x1c {
		t.Errorf("key0 = %#x, want 0x1c", usb[2])
	}

	// A pass-through modifier usage (Right Alt / AltGr, 0xE6) must hit the bitmask.
	rep = s.KeyEventBuild(scancodePassthroughBase|0xE6, true)
	usb = usbFromReport(rep)
	if usb[0] != modRightAlt {
		t.Errorf("after AltGr down: modifier byte = %#x, want %#x", usb[0], modRightAlt)
	}
	if usb[2] != 0x1c {
		t.Errorf("held key dropped: key0 = %#x, want 0x1c", usb[2])
	}

	// Release the key; the slot clears, the modifier stays held.
	rep = s.KeyEventBuild(scancodePassthroughBase|0x1c, false)
	usb = usbFromReport(rep)
	if usb[2] != 0 {
		t.Errorf("after key up: key0 = %#x, want 0", usb[2])
	}
	if usb[0] != modRightAlt {
		t.Errorf("after key up: modifier byte = %#x, want %#x", usb[0], modRightAlt)
	}
}

// TestModifierBitmask drives the sink through a Ctrl+Alt+Del-style sequence and
// checks byte0 of the resulting USB reports.
func TestModifierBitmask(t *testing.T) {
	s := &HIDSink{pressed: make([]byte, 0, 6)}

	// Press Left Ctrl
	s.KeyEventBuild(0xffe3, true)
	if s.mods != modLeftCtrl {
		t.Fatalf("after Ctrl down: mods=%#x want %#x", s.mods, modLeftCtrl)
	}
	// Press Left Alt
	s.KeyEventBuild(0xffe9, true)
	if s.mods != modLeftCtrl|modLeftAlt {
		t.Fatalf("after Alt down: mods=%#x want %#x", s.mods, modLeftCtrl|modLeftAlt)
	}
	// Press Delete (0xffff → usage 0x4c)
	rep := s.KeyEventBuild(0xffff, true)
	usb := usbFromReport(rep)
	if usb[0] != modLeftCtrl|modLeftAlt {
		t.Errorf("report modifier byte = %#x, want %#x", usb[0], modLeftCtrl|modLeftAlt)
	}
	if usb[2] != 0x4c {
		t.Errorf("report key0 = %#x, want 0x4c (Delete)", usb[2])
	}
	// Release Ctrl
	s.KeyEventBuild(0xffe3, false)
	if s.mods != modLeftAlt {
		t.Fatalf("after Ctrl up: mods=%#x want %#x", s.mods, modLeftAlt)
	}
}

// KeyEventBuild applies the key event to sink state and returns the built report
// without touching the network. It mirrors KeyEvent minus the send.
func (s *HIDSink) KeyEventBuild(keysym uint32, down bool) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		return nil
	}
	return s.buildKeyboardReport()
}

func usbFromReport(rep []byte) []byte { return rep[usbDataOff : usbDataOff+8] }

func TestScaleAbs(t *testing.T) {
	cases := []struct {
		name         string
		x, y, w, h   int
		wantX, wantY int16
	}{
		{"origin", 0, 0, 1024, 768, 0, 0},
		{"full", 1024, 768, 1024, 768, 32767, 32767},
		{"center", 512, 384, 1024, 768, 16384, 16384},
		{"clamp-over", 2000, 2000, 1024, 768, 32767, 32767},
		{"clamp-under", -5, -5, 1024, 768, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gx, gy := scaleAbs(c.x, c.y, c.w, c.h)
			if gx != c.wantX || gy != c.wantY {
				t.Errorf("scaleAbs(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.x, c.y, c.w, c.h, gx, gy, c.wantX, c.wantY)
			}
		})
	}
}

func TestKeyboardReportLayout(t *testing.T) {
	usb := [8]byte{modLeftShift, 0, 0x04, 0, 0, 0, 0, 0} // Shift+a
	rep := keyboardReport(usb)
	if len(rep) != HeaderSize+41 {
		t.Fatalf("report len = %d, want %d", len(rep), HeaderSize+41)
	}
	// IVTP header (7-byte dialect)
	if rep[0] != opHIDPkt {
		t.Errorf("ivtp type = %d, want %d", rep[0], opHIDPkt)
	}
	if got := binary.LittleEndian.Uint32(rep[1:5]); got != 41 {
		t.Errorf("ivtp size = %d, want 41", got)
	}
	// signature
	if string(rep[iusbBase:iusbBase+8]) != "IUSB    " {
		t.Errorf("signature = %q, want %q", rep[iusbBase:iusbBase+8], "IUSB    ")
	}
	if rep[iusbBase+8] != 1 || rep[iusbBase+10] != 32 {
		t.Errorf("iusb hdr fields wrong: major=%d size=%d", rep[iusbBase+8], rep[iusbBase+10])
	}
	if rep[iusbBase+17] != iusbDeviceKeybd || rep[iusbBase+18] != iusbProtoKeybd {
		t.Errorf("device/proto = %d/%d, want %d/%d", rep[iusbBase+17], rep[iusbBase+18], iusbDeviceKeybd, iusbProtoKeybd)
	}
	if rep[iusbBase+19] != iusbFromRemote {
		t.Errorf("direction = %#x, want %#x", rep[iusbBase+19], iusbFromRemote)
	}
	if rep[iusbBase+32] != 8 {
		t.Errorf("data length = %d, want 8", rep[iusbBase+32])
	}
	// USB report region
	if rep[usbDataOff] != modLeftShift || rep[usbDataOff+2] != 0x04 {
		t.Errorf("usb report = %v, want mod=%#x key0=0x04", rep[usbDataOff:usbDataOff+8], modLeftShift)
	}
	sumNoCk := 0
	ckOff := iusbBase + 11
	for i := iusbBase; i < iusbBase+32; i++ {
		if i == ckOff {
			continue
		}
		sumNoCk += int(rep[i])
	}
	if rep[ckOff] != byte(-(sumNoCk & 0xFF)) {
		t.Errorf("checksum byte = %#x, want %#x", rep[ckOff], byte(-(sumNoCk & 0xFF)))
	}
}

func TestMouseAbsReportLayout(t *testing.T) {
	rep := mouseAbsReport(0x01, 16384, 16384, 0) // left button, center
	if len(rep) != HeaderSize+39 {
		t.Fatalf("report len = %d, want %d", len(rep), HeaderSize+39)
	}
	if got := binary.LittleEndian.Uint32(rep[1:5]); got != 39 {
		t.Errorf("ivtp size = %d, want 39", got)
	}
	if rep[iusbBase+17] != iusbDeviceMouse || rep[iusbBase+18] != iusbProtoMouse {
		t.Errorf("device/proto = %d/%d, want %d/%d", rep[iusbBase+17], rep[iusbBase+18], iusbDeviceMouse, iusbProtoMouse)
	}
	if rep[iusbBase+21] != 1 {
		t.Errorf("ifnum = %d, want 1 (mouse)", rep[iusbBase+21])
	}
	if rep[iusbBase+32] != 6 {
		t.Errorf("data length = %d, want 6", rep[iusbBase+32])
	}
	if rep[usbDataOff] != 0x01 {
		t.Errorf("button = %#x, want 0x01", rep[usbDataOff])
	}
	if x := int16(binary.LittleEndian.Uint16(rep[usbDataOff+1 : usbDataOff+3])); x != 16384 {
		t.Errorf("abs x = %d, want 16384", x)
	}
	if y := int16(binary.LittleEndian.Uint16(rep[usbDataOff+3 : usbDataOff+5])); y != 16384 {
		t.Errorf("abs y = %d, want 16384", y)
	}
}

func TestMouseButtonMapping(t *testing.T) {
	// Verify RFB→USB button bit remapping (left/right/middle swap of bit1/bit2).
	cases := []struct {
		name    string
		rfbMask uint8
		wantBtn byte
	}{
		{"none", 0x00, 0x00},
		{"left", 0x01, 0x01},
		{"middle", 0x02, 0x04},
		{"right", 0x04, 0x02},
		{"left+right", 0x05, 0x03},
		{"wheel bits ignored", 0x18, 0x00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rfbButtonsToUSB(c.rfbMask); got != c.wantBtn {
				t.Errorf("rfb mask %#x → usb btn %#x, want %#x", c.rfbMask, got, c.wantBtn)
			}
		})
	}
}

func TestWheel(t *testing.T) {
	if rfbWheel(0x08) != 1 {
		t.Errorf("wheel up should be +1")
	}
	if rfbWheel(0x10) != 0xff {
		t.Errorf("wheel down should be -1 (0xff)")
	}
	if rfbWheel(0x01) != 0 {
		t.Errorf("no wheel bit should be 0")
	}
}
