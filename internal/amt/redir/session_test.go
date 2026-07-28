package redir

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseDigestChallenge(t *testing.T) {
	// realm="DigestRealm", nonce="abc123", qop="auth"
	var b []byte
	b = appendLP(b, "DigestRealm")
	b = appendLP(b, "abc123")
	b = appendLP(b, "auth")
	realm, nonce, qop, err := parseDigestChallenge(b, true)
	if err != nil {
		t.Fatal(err)
	}
	if realm != "DigestRealm" || nonce != "abc123" || qop != "auth" {
		t.Fatalf("got realm=%q nonce=%q qop=%q", realm, nonce, qop)
	}
}

func TestMD5HexKnown(t *testing.T) {
	// HA1 for admin:DigestRealm:password
	got := md5hex("admin:DigestRealm:password")
	if len(got) != 32 {
		t.Fatalf("md5hex len=%d", len(got))
	}
}

func TestUnwrapRLEStored(t *testing.T) {
	// 0x00 | len=3 | ~len | payload 0x80 0x00 0xff
	payload := []byte{0x80, 0x00, 0xff}
	var data []byte
	data = append(data, 0x00)
	data = append(data, byte(len(payload)), 0) // LE16 len
	nlen := uint16(^uint16(len(payload)))
	data = append(data, byte(nlen), byte(nlen>>8))
	data = append(data, payload...)
	out, err := unwrapRLE(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("got %x want %x", out, payload)
	}
}

func TestUnwrapRLEZlib(t *testing.T) {
	// Live AMT first-tile shape: zlib header + stored deflate block, truncated trailer.
	// Payload is LRE subencoding 128 (RLE) + color + run bytes.
	lre := []byte{0x80, 0x00}
	for i := 0; i < 48; i++ {
		lre = append(lre, 0xff)
	}
	var raw []byte
	raw = append(raw, 0x00) // BFINAL=0 BTYPE=00 stored
	raw = append(raw, byte(len(lre)), byte(len(lre)>>8))
	nlen := uint16(^uint16(len(lre)))
	raw = append(raw, byte(nlen), byte(nlen>>8))
	raw = append(raw, lre...)
	// zlib CMF/FLG for deflate 32k (no dict); omit Adler32 like AMT often does.
	data := append([]byte{0x78, 0x9c}, raw...)
	out, err := unwrapRLE(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, lre) {
		t.Fatalf("got %x want %x", out, lre)
	}
	// Regression: without unwrap, CMF 0x78 was reported as "LRE subencoding 120".
	if out[0] == 0x78 {
		t.Fatal("still treating zlib CMF as LRE")
	}
}

func TestBlitRAW16(t *testing.T) {
	c := newRFBClient(bytes.NewBuffer(nil))
	c.resize(2, 2)
	// one red-ish RGB565 pixel: R=31 → 0xf800
	pix := []byte{0x00, 0xf8} // LE 0xf800
	c.blitRAW(0, 0, 1, 1, pix)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fb[0] != 0xf8 || c.fb[1] != 0x00 || c.fb[2] != 0x00 {
		t.Fatalf("pixel RGB = %02x %02x %02x want f8 00 00", c.fb[0], c.fb[1], c.fb[2])
	}
}

func TestRFBHandshakeFixture(t *testing.T) {
	// Minimal fake AMT RFB server responses after redirection auth.
	var srv bytes.Buffer
	srv.WriteString("RFB 003.008\n")
	srv.WriteByte(1) // 1 security type
	srv.WriteByte(1) // None
	binary.Write(&srv, binary.BigEndian, uint32(0)) // security OK
	// ServerInit: 800x600, pixel format stub, name "AMT"
	var init [24]byte
	binary.BigEndian.PutUint16(init[0:2], 800)
	binary.BigEndian.PutUint16(init[2:4], 600)
	init[4] = 16 // bits-per-pixel
	init[5] = 16
	init[7] = 1 // true-color
	binary.BigEndian.PutUint32(init[20:24], 3)
	srv.Write(init[:])
	srv.WriteString("AMT")

	rw := &rwBuf{r: &srv}
	c := newRFBClient(rw)
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	w, h := c.Size()
	if w != 800 || h != 600 {
		t.Fatalf("size=%dx%d", w, h)
	}
	// Client should have sent version, security None, shared flag, SetEncodings, FBUR
	if !bytes.Contains(rw.w.Bytes(), []byte("RFB 003.008\n")) {
		t.Fatal("missing client version")
	}
}

// rwBuf is a half-duplex ReadWriter for fixtures.
type rwBuf struct {
	r *bytes.Buffer
	w bytes.Buffer
}

func (b *rwBuf) Read(p []byte) (int, error)  { return b.r.Read(p) }
func (b *rwBuf) Write(p []byte) (int, error) { return b.w.Write(p) }
