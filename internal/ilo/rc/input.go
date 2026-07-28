package rc

// Client → server input records (cleartext; Channel.Send applies encryption).

const (
	recPower    = 0x00
	recKeyboard = 0x01
	recMouse    = 0x02
	recRefresh  = 0x05
	recAck      = 0x0C

	mouseLeft   = 0x02
	mouseCenter = 0x04
	mouseRight  = 0x01
)

// KeyboardReport builds a 10-byte USB-HID keyboard record.
func KeyboardReport(modifiers byte, keys []byte) []byte {
	frame := make([]byte, 10)
	frame[0] = recKeyboard
	frame[2] = modifiers
	for i := 0; i < len(keys) && i < 6; i++ {
		frame[4+i] = keys[i]
	}
	return frame
}

// KeyboardRelease is an all-keys-up report.
func KeyboardRelease() []byte {
	return KeyboardReport(0, nil)
}

// MouseAbsolute builds a 10-byte absolute mouse record scaled to 0..3000.
func MouseAbsolute(x, y, screenX, screenY int, dx, dy int, buttons byte) []byte {
	wx, wy := 0, 0
	if screenX > 0 {
		wx = 3000 * x / screenX
	}
	if screenY > 0 {
		wy = 3000 * y / screenY
	}
	if dx < -127 {
		dx = -127
	} else if dx > 127 {
		dx = 127
	}
	if dy < -127 {
		dy = -127
	} else if dy > 127 {
		dy = 127
	}
	return []byte{
		recMouse, 0x00,
		byte(wx), byte(wx >> 8),
		byte(wy), byte(wy >> 8),
		byte(dx), byte(dy),
		buttons, 0x00,
	}
}

// RefreshScreen requests a full redraw.
func RefreshScreen() []byte { return []byte{recRefresh, 0x00} }

// Ack answers DVC command 16.
func Ack() []byte { return []byte{recAck, 0x00} }

// RFBButtonsToILO maps RFB button bits (1=left,2=middle,4=right) to iLO order.
func RFBButtonsToILO(rfbButtons uint8) byte {
	var b byte
	if rfbButtons&0x01 != 0 {
		b |= mouseLeft
	}
	if rfbButtons&0x02 != 0 {
		b |= mouseCenter
	}
	if rfbButtons&0x04 != 0 {
		b |= mouseRight
	}
	return b
}
