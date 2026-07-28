package redir

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// rfbClient speaks RFB to an AMT KVM stream (after redirection auth).
// AMT advertises RFB 004.000; we reply 003.008 (MeshCommander). Default
// pixels are RGB565 (2 bpp). Prefer AMT RLE (encoding 16) — RAW full-frames
// at 4K exceed the ME framebuffer budget and reset the session.
type rfbClient struct {
	rw  io.ReadWriter
	bpp int // 1 or 2
	w, h int
	fb  []byte // RGBX

	mu      sync.Mutex
	onFrame func(w, h int, pix []byte)
}

func newRFBClient(rw io.ReadWriter) *rfbClient {
	return &rfbClient{rw: rw, bpp: 2}
}

func (c *rfbClient) SetOnFrame(fn func(w, h int, pix []byte)) {
	c.mu.Lock()
	c.onFrame = fn
	c.mu.Unlock()
}

func (c *rfbClient) Size() (w, h int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w, c.h
}

// Handshake completes RFB init and requests RLE + RAW + DesktopSize.
func (c *rfbClient) Handshake() error {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(c.rw, ver); err != nil {
		return fmt.Errorf("rfb version: %w", err)
	}
	if _, err := io.WriteString(c.rw, "RFB 003.008\n"); err != nil {
		return err
	}
	var nTypes [1]byte
	if _, err := io.ReadFull(c.rw, nTypes[:]); err != nil {
		return err
	}
	types := make([]byte, nTypes[0])
	if _, err := io.ReadFull(c.rw, types); err != nil {
		return err
	}
	if _, err := c.rw.Write([]byte{1}); err != nil { // None
		return err
	}
	var secResult [4]byte
	if _, err := io.ReadFull(c.rw, secResult[:]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(secResult[:]) != 0 {
		return fmt.Errorf("rfb security failed: %v", secResult)
	}
	if _, err := c.rw.Write([]byte{1}); err != nil { // shared
		return err
	}
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(c.rw, hdr); err != nil {
		return err
	}
	w := int(binary.BigEndian.Uint16(hdr[0:2]))
	h := int(binary.BigEndian.Uint16(hdr[2:4]))
	if bpp := int(hdr[4]); bpp == 8 {
		c.bpp = 1
	} else {
		c.bpp = 2
	}
	nameLen := int(binary.BigEndian.Uint32(hdr[20:24]))
	if nameLen > 0 {
		if _, err := io.CopyN(io.Discard, c.rw, int64(nameLen)); err != nil {
			return err
		}
	}
	c.resize(w, h)

	// 4K RGB565 exceeds AMT's ~9MB KVM buffer; drop to RGB332 like MeshCommander.
	if c.bpp*w*h > 9216000 {
		c.bpp = 1
		// SetPixelFormat: 8bpp, depth 8, LE, true-color, RGB332
		pf := []byte{
			0, 0, 0, 0,
			8, 8, 0, 1,
			0, 7, 0, 7, 0, 3,
			5, 2, 0,
			0, 0, 0,
		}
		if _, err := c.rw.Write(pf); err != nil {
			return err
		}
	}

	// RLE (16), RAW (0), DesktopSize (-223) — RLE first for large desktops.
	enc := []byte{
		2, 0, // SetEncodings
		0, 3,
		0, 0, 0, 16, // RLE
		0, 0, 0, 0, // RAW
		0xff, 0xff, 0xff, 0x21, // DesktopSize
	}
	if _, err := c.rw.Write(enc); err != nil {
		return err
	}
	return c.requestUpdate(false)
}

func (c *rfbClient) resize(w, h int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.w, c.h = w, h
	c.fb = make([]byte, w*h*4)
	for i := 3; i < len(c.fb); i += 4 {
		c.fb[i] = 0xff
	}
}

func (c *rfbClient) requestUpdate(incremental bool) error {
	c.mu.Lock()
	w, h := c.w, c.h
	c.mu.Unlock()
	inc := byte(0)
	if incremental {
		inc = 1
	}
	msg := []byte{3, inc, 0, 0, 0, 0, byte(w >> 8), byte(w), byte(h >> 8), byte(h)}
	_, err := c.rw.Write(msg)
	return err
}

// Run reads FramebufferUpdates until error/EOF.
func (c *rfbClient) Run() error {
	for {
		var typ [1]byte
		if _, err := io.ReadFull(c.rw, typ[:]); err != nil {
			return err
		}
		switch typ[0] {
		case 0: // FramebufferUpdate
			var hdr [3]byte
			if _, err := io.ReadFull(c.rw, hdr[:]); err != nil {
				return err
			}
			nRects := int(binary.BigEndian.Uint16(hdr[1:3]))
			for i := 0; i < nRects; i++ {
				if err := c.readRect(); err != nil {
					return err
				}
			}
			c.emit()
			if err := c.requestUpdate(true); err != nil {
				return err
			}
		case 2: // Bell
			continue
		case 3: // ServerCutText
			var pad [3]byte
			if _, err := io.ReadFull(c.rw, pad[:]); err != nil {
				return err
			}
			var ln [4]byte
			if _, err := io.ReadFull(c.rw, ln[:]); err != nil {
				return err
			}
			n := binary.BigEndian.Uint32(ln[:])
			if _, err := io.CopyN(io.Discard, c.rw, int64(n)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("rfb unknown server msg %d", typ[0])
		}
	}
}

func (c *rfbClient) readRect() error {
	hdr := make([]byte, 12)
	if _, err := io.ReadFull(c.rw, hdr); err != nil {
		return err
	}
	x := int(binary.BigEndian.Uint16(hdr[0:2]))
	y := int(binary.BigEndian.Uint16(hdr[2:4]))
	w := int(binary.BigEndian.Uint16(hdr[4:6]))
	h := int(binary.BigEndian.Uint16(hdr[6:8]))
	enc := int32(binary.BigEndian.Uint32(hdr[8:12]))

	switch enc {
	case -223: // DesktopSize
		c.resize(w, h)
		return c.requestUpdate(false)
	case 0: // RAW
		n := w * h * c.bpp
		data := make([]byte, n)
		if _, err := io.ReadFull(c.rw, data); err != nil {
			return err
		}
		c.blitRAW(x, y, w, h, data)
		return nil
	case 16: // AMT RLE (zlib-wrapped LRE tile)
		return c.readRLE(x, y, w, h)
	default:
		return fmt.Errorf("rfb unsupported encoding %d", enc)
	}
}

func (c *rfbClient) readRLE(x, y, w, h int) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.rw, lenBuf[:]); err != nil {
		return err
	}
	datalen := int(binary.BigEndian.Uint32(lenBuf[:]))
	if datalen < 0 || datalen > 8<<20 {
		return fmt.Errorf("rle datalen %d", datalen)
	}
	data := make([]byte, datalen)
	if _, err := io.ReadFull(c.rw, data); err != nil {
		return err
	}
	payload, err := unwrapRLE(data)
	if err != nil {
		return err
	}
	return c.decodeLRE(payload, x, y, w, h)
}

// unwrapRLE strips AMT's zlib framing around an LRE tile, matching MeshCommander
// amt-desktop.js. AMT sends either:
//   1. Stored block: 0x00 | len(LE16) | ~len | payload  (most tiles)
//   2. zlib-wrapped deflate (often first tile: CMF/FLG 0x78 0x9c …)
// Treating (2) as raw LRE reads CMF 0x78 as "subencoding 120" and aborts the session.
func unwrapRLE(data []byte) ([]byte, error) {
	datalen := len(data)
	if datalen == 0 {
		return nil, fmt.Errorf("empty rle payload")
	}
	// Uncompressed zlib stored-block shortcut (MeshCommander).
	if datalen > 5 && data[0] == 0 {
		declared := int(binary.LittleEndian.Uint16(data[1:3]))
		if declared == datalen-5 {
			return data[5:], nil
		}
	}
	// zlib member (deflate method in low nibble of CMF).
	if data[0]&0x0f == 8 {
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("rle zlib header: %w", err)
		}
		out, err := io.ReadAll(zr)
		_ = zr.Close()
		// AMT often omits a clean zlib trailer; accept any decompressed bytes.
		if len(out) > 0 {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("rle zlib: %w", err)
		}
		return nil, fmt.Errorf("rle zlib: empty output")
	}
	// Already bare LRE (or unknown framing) — try as-is.
	return data, nil
}

func (c *rfbClient) decodeLRE(data []byte, x, y, tw, th int) error {
	if len(data) < 1 {
		return fmt.Errorf("empty LRE")
	}
	sub := data[0]
	ptr := 1
	s := tw * th
	tile := make([]uint16, s) // store RGB565 values (or RGB332 in low byte)

	setPix := func(i int, v uint16) {
		if i >= 0 && i < s {
			tile[i] = v
		}
	}
	readColor := func() (uint16, error) {
		if c.bpp == 2 {
			if ptr+1 >= len(data) {
				return 0, io.ErrUnexpectedEOF
			}
			v := uint16(data[ptr]) | uint16(data[ptr+1])<<8
			ptr += 2
			return v, nil
		}
		if ptr >= len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		v := uint16(data[ptr])
		ptr++
		return v, nil
	}

	switch {
	case sub == 0: // RAW tile
		for i := 0; i < s; i++ {
			v, err := readColor()
			if err != nil {
				return err
			}
			setPix(i, v)
		}
	case sub == 1: // solid
		v, err := readColor()
		if err != nil {
			return err
		}
		for i := 0; i < s; i++ {
			setPix(i, v)
		}
	case sub > 1 && sub < 17: // packed palette
		nPal := int(sub)
		pal := make([]uint16, nPal)
		for i := 0; i < nPal; i++ {
			v, err := readColor()
			if err != nil {
				return err
			}
			pal[i] = v
		}
		br, bm := 4, 15
		if nPal == 2 {
			br, bm = 1, 1
		} else if nPal <= 4 {
			br, bm = 2, 3
		}
		rlecount := 0
		for rlecount < s && ptr < len(data) {
			v := data[ptr]
			ptr++
			for i := 8 - br; i >= 0 && rlecount < s; i -= br {
				setPix(rlecount, pal[(int(v)>>i)&bm])
				rlecount++
			}
		}
	case sub == 128: // RLE
		rlecount := 0
		for rlecount < s && ptr < len(data) {
			v, err := readColor()
			if err != nil {
				return err
			}
			run := 1
			for {
				if ptr >= len(data) {
					return io.ErrUnexpectedEOF
				}
				d := int(data[ptr])
				ptr++
				run += d
				if d != 255 {
					break
				}
			}
			for j := 0; j < run && rlecount < s; j++ {
				setPix(rlecount, v)
				rlecount++
			}
		}
	case sub > 129: // palette RLE
		n := int(sub) - 128
		pal := make([]uint16, n)
		for i := 0; i < n; i++ {
			v, err := readColor()
			if err != nil {
				return err
			}
			pal[i] = v
		}
		rlecount := 0
		for rlecount < s && ptr < len(data) {
			idx := data[ptr]
			ptr++
			v := pal[int(idx)%128]
			run := 1
			if idx > 127 {
				for {
					if ptr >= len(data) {
						return io.ErrUnexpectedEOF
					}
					d := int(data[ptr])
					ptr++
					run += d
					if d != 255 {
						break
					}
				}
			}
			for j := 0; j < run && rlecount < s; j++ {
				setPix(rlecount, v)
				rlecount++
			}
		}
	default:
		return fmt.Errorf("LRE subencoding %d", sub)
	}

	// Blit tile RGB565 → framebuffer RGBX
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fb == nil {
		return nil
	}
	for row := 0; row < th; row++ {
		dy := y + row
		if dy < 0 || dy >= c.h {
			continue
		}
		for col := 0; col < tw; col++ {
			dx := x + col
			if dx < 0 || dx >= c.w {
				continue
			}
			v := tile[row*tw+col]
			off := (dy*c.w + dx) * 4
			if c.bpp == 2 {
				c.fb[off] = byte((v >> 8) & 248)
				c.fb[off+1] = byte((v >> 3) & 252)
				c.fb[off+2] = byte((v & 31) << 3)
			} else {
				c.fb[off] = byte(v) & 0xe0
				c.fb[off+1] = (byte(v) & 0x1c) << 3
				c.fb[off+2] = (byte(v) & 0x03) << 6
			}
			c.fb[off+3] = 0xff
		}
	}
	return nil
}

func (c *rfbClient) blitRAW(x, y, tw, th int, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fb == nil || tw < 1 || th < 1 {
		return
	}
	ptr := 0
	for row := 0; row < th; row++ {
		dy := y + row
		if dy < 0 || dy >= c.h {
			ptr += tw * c.bpp
			continue
		}
		for col := 0; col < tw; col++ {
			dx := x + col
			if dx < 0 || dx >= c.w {
				ptr += c.bpp
				continue
			}
			off := (dy*c.w + dx) * 4
			if c.bpp == 2 {
				if ptr+1 >= len(data) {
					return
				}
				v := uint16(data[ptr]) | uint16(data[ptr+1])<<8
				ptr += 2
				c.fb[off] = byte((v >> 8) & 248)
				c.fb[off+1] = byte((v >> 3) & 252)
				c.fb[off+2] = byte((v & 31) << 3)
				c.fb[off+3] = 0xff
			} else {
				if ptr >= len(data) {
					return
				}
				v := data[ptr]
				ptr++
				c.fb[off] = v & 0xe0
				c.fb[off+1] = (v & 0x1c) << 3
				c.fb[off+2] = (v & 0x03) << 6
				c.fb[off+3] = 0xff
			}
		}
	}
}

func (c *rfbClient) emit() {
	c.mu.Lock()
	fn := c.onFrame
	w, h := c.w, c.h
	pix := append([]byte(nil), c.fb...)
	c.mu.Unlock()
	if fn != nil && len(pix) > 0 {
		fn(w, h, pix)
	}
}

func (c *rfbClient) KeyEvent(keysym uint32, down bool) error {
	d := byte(0)
	if down {
		d = 1
	}
	var msg [8]byte
	msg[0] = 4
	msg[1] = d
	binary.BigEndian.PutUint32(msg[4:], keysym)
	_, err := c.rw.Write(msg[:])
	return err
}

func (c *rfbClient) PointerEvent(x, y int, buttons uint8) error {
	var msg [6]byte
	msg[0] = 5
	msg[1] = buttons
	binary.BigEndian.PutUint16(msg[2:], uint16(x))
	binary.BigEndian.PutUint16(msg[4:], uint16(y))
	_, err := c.rw.Write(msg[:])
	return err
}
