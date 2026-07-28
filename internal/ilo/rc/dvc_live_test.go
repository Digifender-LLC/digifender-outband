package rc

import (
	"encoding/hex"
	"testing"
)

type headerCtl struct {
	NopController
	cipher int
	resize [2]int
}

func (c *headerCtl) SetCipher(cipher int) { c.cipher = cipher }
func (c *headerCtl) OnLicensed(int)       {}
func (c *headerCtl) OnFlags(int)          {}
func (c *headerCtl) OnResize(w, h int)    { c.resize = [2]int{w, h} }
func (c *headerCtl) OnStatus(int, string) {}

func TestDVCHeaderFromLiveCapture(t *testing.T) {
	// First 16 plaintext bytes from live iLO 4 (after per-byte RC4 enable at byte 6).
	plain, err := hex.DecodeString("1d2060881061b50000000000808d9609")
	if err != nil {
		t.Fatal(err)
	}
	fb := &Framebuffer{}
	ctl := &headerCtl{}
	d := NewDecoder(fb, ctl)
	for _, b := range plain {
		_ = d.Feed(b)
	}
	if ctl.cipher != 1 {
		t.Fatalf("cipher=%d want RC4(1)", ctl.cipher)
	}
}

func TestBitReversalTable(t *testing.T) {
	d := NewDecoder(&Framebuffer{}, nil)
	if d.reversal[0x80] != 0x01 {
		t.Fatalf("reversal[0x80]=%#x want 0x01", d.reversal[0x80])
	}
	if d.reversal[0x01] != 0x80 {
		t.Fatalf("reversal[0x01]=%#x want 0x80", d.reversal[0x01])
	}
	if d.reversal[0x1d] != 0xb8 {
		t.Fatalf("reversal[0x1d]=%#x want 0xb8", d.reversal[0x1d])
	}
}
