//go:build ignore
package main

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	host, user, pass := os.Getenv("OUTBAND_BMC_HOST"), os.Getenv("OUTBAND_BMC_USER"), os.Getenv("OUTBAND_BMC_PASS")
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "16994"), 5*time.Second)
	must(err)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	send := func(b []byte, label string) {
		fmt.Printf(">> %s (%d)\n", label, len(b))
		must(writeAll(conn, b))
	}
	recvN := func(n int) []byte {
		buf := make([]byte, n)
		_, err := io.ReadFull(conn, buf)
		fmt.Printf("<< want=%d err=%v hex=%s ascii=%q\n", n, err, hex.EncodeToString(buf[:min(64,n)]), clip(string(buf), 40))
		must(err)
		return buf
	}
	recvSome := func() []byte {
		buf := make([]byte, 8192)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := conn.Read(buf)
		fmt.Printf("<< some n=%d err=%v hex=%s\n", n, err, hex.EncodeToString(buf[:min(64,max(0,n))]))
		return buf[:max(0,n)]
	}

	// redir handshake (compact)
	must(writeAll(conn, []byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}))
	b := recvN(13)
	oem := int(b[12]); if oem > 0 { recvN(oem) }
	must(writeAll(conn, []byte{0x13, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}))
	b = make([]byte, 9); must(readFull(conn, b))
	al := int(binary.LittleEndian.Uint32(b[5:9])); ad := make([]byte, al); must(readFull(conn, ad))
	uri := "/RedirectionService"
	tot := len(user) + len(uri) + 8
	msg := []byte{0x13, 0x00, 0x00, 0x00, 0x04}
	msg = appendU32(msg, uint32(tot))
	msg = append(msg, byte(len(user))); msg = append(msg, user...)
	msg = append(msg, 0, 0)
	msg = append(msg, byte(len(uri))); msg = append(msg, uri...)
	msg = append(msg, 0, 0, 0, 0)
	must(writeAll(conn, msg))
	b = make([]byte, 9); must(readFull(conn, b))
	al = int(binary.LittleEndian.Uint32(b[5:9])); ad = make([]byte, al); must(readFull(conn, ad))
	at := b[4]
	cur := 0
	readLP := func() string { l := int(ad[cur]); cur++; s := string(ad[cur : cur+l]); cur += l; return s }
	realm, nonce := readLP(), readLP(); qop := ""; if at == 4 { qop = readLP() }
	cnonce := randomHex(16); snc := "00000002"
	extra := ""; if at == 4 { extra = snc + ":" + cnonce + ":" + qop + ":" }
	digest := md5hex(md5hex(user+":"+realm+":"+pass) + ":" + nonce + ":" + extra + md5hex("POST:"+uri))
	totallen := len(user)+len(realm)+len(nonce)+len(uri)+len(cnonce)+len(snc)+len(digest)+7
	if at == 4 { totallen += len(qop) + 1 }
	msg = []byte{0x13, 0x00, 0x00, 0x00, at}
	msg = appendU32(msg, uint32(totallen))
	for _, s := range []string{user, realm, nonce, uri, cnonce, snc, digest} {
		msg = append(msg, byte(len(s))); msg = append(msg, s...)
	}
	if at == 4 { msg = append(msg, byte(len(qop))); msg = append(msg, qop...) }
	must(writeAll(conn, msg))
	b = make([]byte, 9); must(readFull(conn, b))
	fmt.Printf("auth ok status=%d\n", b[1])
	must(writeAll(conn, []byte{0x40, 0, 0, 0, 0, 0, 0, 0}))
	recvN(8) // 0x41

	fmt.Println("=== RFB ===")
	ver := recvN(12)
	fmt.Printf("server ver %q\n", string(ver))
	send([]byte("RFB 003.008\n"), "client ver")
	nTypes := recvN(1)
	types := recvN(int(nTypes[0]))
	fmt.Printf("sec types %v\n", types)
	send([]byte{1}, "None")
	sec := recvN(4)
	fmt.Printf("sec result %v\n", sec)
	send([]byte{1}, "shared")
	init := recvN(24)
	w := binary.BigEndian.Uint16(init[0:2])
	h := binary.BigEndian.Uint16(init[2:4])
	nameLen := binary.BigEndian.Uint32(init[20:24])
	name := recvN(int(nameLen))
	fmt.Printf("serverinit %dx%d bpp=%d name=%q\n", w, h, init[4], string(name))

	// SetEncodings: RAW + DesktopSize like our client
	enc := []byte{2, 0, 0, 3, 0, 0, 0, 16, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0x21}
	send(enc, "SetEncodings RLE+RAW+DS")
	// FBUR full
	fbur := []byte{3, 0, 0, 0, 0, 0, byte(w >> 8), byte(w), byte(h >> 8), byte(h)}
	// Try 8-bit RGB332 to fit AMT buffer (bpp*w*h <= 9216000)
	pf := []byte{0, 0, 0, 0, 8, 8, 0, 1, 0, 7, 0, 7, 0, 3, 5, 2, 0, 0, 0, 0}
	send(pf, "SetPixelFormat RGB332")
	send(fbur, "FBUR")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	// Drain DesktopSize update then request again
	total := 0
	for i := 0; i < 10; i++ {
		chunk := recvSome()
		if len(chunk) == 0 { break }
		total += len(chunk)
		fmt.Printf("chunk%d len=%d first=%d\n", i, len(chunk), chunk[0])
	}
	fmt.Printf("drained %d bytes, sending FBUR again\n", total)
	send(fbur, "FBUR2")
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	rects := 0
	for i := 0; i < 50; i++ {
		chunk := recvSome()
		if len(chunk) == 0 { break }
		rects++
		fmt.Printf("data%d len=%d first=%d hex=%x\n", i, len(chunk), chunk[0], chunk[:min(24,len(chunk))])
	}
	fmt.Printf("got %d subsequent chunks\n", rects)
}

func must(err error) { if err != nil { panic(err) } }
func writeAll(c net.Conn, b []byte) error { _, err := c.Write(b); return err }
func readFull(c net.Conn, b []byte) error { _, err := io.ReadFull(c, b); return err }
func appendU32(b []byte, v uint32) []byte { var t [4]byte; binary.LittleEndian.PutUint32(t[:], v); return append(b, t[:]...) }
func md5hex(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func min(a, b int) int { if a < b { return a }; return b }
func max(a, b int) int { if a > b { return a }; return b }
func clip(s string, n int) string { if len(s) > n { return s[:n] }; return s }
