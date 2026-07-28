package rc

import (
	"testing"
)

type recordController struct {
	NopController
	cipher int
	ack    int
	status []struct {
		field int
		text  string
	}
	resize [][2]int
	exit   int
}

func (c *recordController) SetCipher(cipher int) { c.cipher = cipher }
func (c *recordController) SendAck()             { c.ack++ }
func (c *recordController) OnStatus(field int, text string) {
	c.status = append(c.status, struct {
		field int
		text  string
	}{field, text})
}
func (c *recordController) OnResize(w, h int) { c.resize = append(c.resize, [2]int{w, h}) }
func (c *recordController) OnExit()           { c.exit++ }

func TestTransitionTableLengths(t *testing.T) {
	if len(bitsToReadInit) != 48 {
		t.Fatalf("bitsToReadInit len = %d, want 48", len(bitsToReadInit))
	}
	if len(next0) != 48 {
		t.Fatalf("next0 len = %d, want 48", len(next0))
	}
	if len(next1Init) != 48 {
		t.Fatalf("next1Init len = %d, want 48", len(next1Init))
	}
	if len(getMask) != 9 {
		t.Fatalf("getMask len = %d, want 9", len(getMask))
	}
}

func TestNewDecoderResetState(t *testing.T) {
	fb := &Framebuffer{}
	d := NewDecoder(fb, NopController{})

	if d.state != reset {
		t.Fatalf("initial state = %d, want reset (%d)", d.state, reset)
	}
	if d.ccActive != 0 {
		t.Fatalf("ccActive = %d, want 0", d.ccActive)
	}
	if d.timeoutCount != -1 {
		t.Fatalf("timeoutCount = %d, want -1", d.timeoutCount)
	}
	if d.bitsToRead != bitsToReadInit {
		t.Fatal("bitsToRead should be a copy of bitsToReadInit")
	}
	if d.next1 != next1Init {
		t.Fatal("next1 should be a copy of next1Init")
	}
}

func TestFeedZeroBytesNoPanic(t *testing.T) {
	fb := &Framebuffer{}
	d := NewDecoder(fb, NopController{})

	for i := 0; i < 256; i++ {
		_ = d.Feed(0)
	}
}

func TestFeedEmptyFramebuffer(t *testing.T) {
	fb := &Framebuffer{}
	d := NewDecoder(fb, nil) // nil ctl → NopController

	if rc := d.Feed(0xFF); rc != 0 && rc != 4 && rc != 6 {
		t.Fatalf("Feed returned unexpected code %d", rc)
	}
}

func TestResetTransition(t *testing.T) {
	fb := &Framebuffer{}
	d := NewDecoder(fb, NopController{})

	d.lastx = 5
	d.lasty = 7
	d.fatalCount = 100
	d.cmdCount = 3
	d.ccActive = 4
	d.dispatch(reset)

	if d.lastx != 0 || d.lasty != 0 {
		t.Fatalf("reset did not zero cursor: lastx=%d lasty=%d", d.lastx, d.lasty)
	}
	if d.fatalCount != 0 {
		t.Fatalf("fatalCount = %d, want 0", d.fatalCount)
	}
	if d.timeoutCount != -1 {
		t.Fatalf("timeoutCount = %d, want -1", d.timeoutCount)
	}
	if d.cmdCount != 0 {
		t.Fatalf("cmdCount = %d, want 0", d.cmdCount)
	}
	if d.ccActive != 0 {
		t.Fatalf("ccActive = %d, want 0", d.ccActive)
	}
}

func TestMode2NoVideo(t *testing.T) {
	fb := &Framebuffer{Width: 640, Height: 480, Pixels: make([]uint32, 640*480)}
	genBefore := fb.Generation
	ctl := &recordController{}
	d := NewDecoder(fb, ctl)

	d.sizeX = 0
	d.sizeY = 0
	d.code = 0
	d.mode2()

	if d.videoDetected {
		t.Fatal("videoDetected should be false for zero screen size")
	}
	if fb.Generation <= genBefore {
		t.Fatal("expected framebuffer clear to bump generation")
	}
	if len(ctl.status) != 1 || ctl.status[0].text != "no video" {
		t.Fatalf("OnStatus = %#v, want no video", ctl.status)
	}
	if len(ctl.resize) != 0 {
		t.Fatalf("OnResize should not fire for no video: %#v", ctl.resize)
	}
}

func TestDispatchCommandCipher(t *testing.T) {
	ctl := &recordController{}
	d := NewDecoder(&Framebuffer{}, ctl)

	d.cmdLast = 12
	d.cmdBuf[0] = CipherRC4
	d.dispatchCommand()
	if ctl.cipher != CipherRC4 {
		t.Fatalf("SetCipher = %d, want RC4 (%d)", ctl.cipher, CipherRC4)
	}

	d.cmdLast = 13
	d.cmdBuf[0] = 2 // bits-per-color selector
	d.cmdBuf[1] = CipherAES128
	d.cmdBuf[2] = 0xAB
	d.cmdBuf[3] = 0xCD
	d.dispatchCommand()
	if ctl.cipher != CipherAES128 {
		t.Fatalf("SetCipher = %d, want AES128 (%d)", ctl.cipher, CipherAES128)
	}
	if d.bitsPerColor != 3 { // 5 - (2 & 3)
		t.Fatalf("bitsPerColor = %d, want 3", d.bitsPerColor)
	}
}

func TestDispatchCommandAckAndClear(t *testing.T) {
	ctl := &recordController{}
	fb := &Framebuffer{Width: 4, Height: 4, Pixels: make([]uint32, 16)}
	for i := range fb.Pixels {
		fb.Pixels[i] = 0xFFFFFF
	}
	d := NewDecoder(fb, ctl)

	d.cmdLast = 16
	d.dispatchCommand()
	if ctl.ack != 1 {
		t.Fatalf("SendAck count = %d, want 1", ctl.ack)
	}

	gen := fb.Generation
	d.cmdLast = 6
	d.dispatchCommand()
	if fb.Generation <= gen {
		t.Fatal("op 6 should clear framebuffer")
	}
}

func TestFramebufferPasteAndRGBX(t *testing.T) {
	fb := &Framebuffer{Width: 32, Height: 16}
	fb.Resize(32, 16)

	block := make([]uint32, 256)
	for i := range block {
		block[i] = 0x00FF8040
	}
	fb.PasteBlock(block, 8, 4, 16, 16)

	if fb.Pixels[4*32+8] != 0x00FF8040 {
		t.Fatalf("paste pixel = 0x%06X, want 0xFF8040", fb.Pixels[4*32+8]&0xFFFFFF)
	}

	rgbx := fb.ToRGBX()
	if len(rgbx) != 32*16*4 {
		t.Fatalf("ToRGBX len = %d, want %d", len(rgbx), 32*16*4)
	}
	if rgbx[(4*32+8)*4] != 0xFF || rgbx[(4*32+8)*4+1] != 0x80 || rgbx[(4*32+8)*4+2] != 0x40 {
		t.Fatalf("RGBX at block origin = [%02x %02x %02x], want ff 80 40",
			rgbx[(4*32+8)*4], rgbx[(4*32+8)*4+1], rgbx[(4*32+8)*4+2])
	}
}

func TestBitsToReadMutationIndependent(t *testing.T) {
	d1 := NewDecoder(&Framebuffer{}, NopController{})
	d2 := NewDecoder(&Framebuffer{}, NopController{})

	d1.bitsToRead[pixgrey] = 99
	if d2.bitsToRead[pixgrey] == 99 {
		t.Fatal("bitsToRead should be per-decoder, not shared")
	}
}
