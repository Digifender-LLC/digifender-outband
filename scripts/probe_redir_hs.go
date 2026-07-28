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
	host := os.Getenv("OUTBAND_BMC_HOST")
	user := os.Getenv("OUTBAND_BMC_USER")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "16994"), 5*time.Second)
	if err != nil { panic(err) }
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	send := func(b []byte, label string) {
		fmt.Printf(">> %s %s\n", label, hex.EncodeToString(b))
		if _, err := conn.Write(b); err != nil { panic(err) }
	}
	recv := func() []byte {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		fmt.Printf("<< n=%d err=%v hex=%s\n", n, err, hex.EncodeToString(buf[:max(0,n)]))
		if err != nil && err != io.EOF { panic(err) }
		if n == 0 { return nil }
		return buf[:n]
	}

	send([]byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}, "start")
	data := recv()
	if len(data) < 13 || data[0] != 0x11 || data[1] != 0 {
		fmt.Println("bad start reply"); return
	}
	oem := int(data[12])
	fmt.Printf("oemlen=%d\n", oem)
	send([]byte{0x13, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, "auth query")
	data = recv()
	if len(data) < 9 || data[0] != 0x14 { fmt.Println("bad auth query reply"); return }
	authType := data[4]
	authLen := int(binary.LittleEndian.Uint32(data[5:9]))
	authData := data[9 : 9+authLen]
	fmt.Printf("authType=%d authData=%v\n", authType, authData)

	// empty digest request
	uri := "/RedirectionService"
	tot := len(user) + len(uri) + 8
	var b []byte
	b = append(b, 0x13, 0x00, 0x00, 0x00, 0x04)
	var le [4]byte
	binary.LittleEndian.PutUint32(le[:], uint32(tot))
	b = append(b, le[:]...)
	b = append(b, byte(len(user)))
	b = append(b, user...)
	b = append(b, 0x00, 0x00)
	b = append(b, byte(len(uri)))
	b = append(b, uri...)
	b = append(b, 0x00, 0x00, 0x00, 0x00)
	send(b, "digest empty")
	data = recv()
	if len(data) < 9 || data[0] != 0x14 { fmt.Println("bad challenge"); return }
	status, at := data[1], data[4]
	al := int(binary.LittleEndian.Uint32(data[5:9]))
	ad := data[9 : 9+al]
	fmt.Printf("challenge status=%d authType=%d\n", status, at)
	cur := 0
	readLP := func() string {
		l := int(ad[cur]); cur++; s := string(ad[cur:cur+l]); cur+=l; return s
	}
	realm := readLP(); nonce := readLP(); qop := ""
	if at == 4 { qop = readLP() }
	fmt.Printf("realm=%q nonce=%q qop=%q\n", realm, nonce, qop)
	cnonce := randomHex(16)
	snc := "00000002"
	extra := ""
	if at == 4 { extra = snc + ":" + cnonce + ":" + qop + ":" }
	digest := md5hex(md5hex(user+":"+realm+":"+pass) + ":" + nonce + ":" + extra + md5hex("POST:"+uri))
	totallen := len(user)+len(realm)+len(nonce)+len(uri)+len(cnonce)+len(snc)+len(digest)+7
	if at == 4 { totallen += len(qop)+1 }
	b = nil
	b = append(b, 0x13, 0x00, 0x00, 0x00, at)
	binary.LittleEndian.PutUint32(le[:], uint32(totallen))
	b = append(b, le[:]...)
	appendLP := func(s string) { b = append(b, byte(len(s))); b = append(b, s...) }
	appendLP(user); appendLP(realm); appendLP(nonce); appendLP(uri); appendLP(cnonce); appendLP(snc); appendLP(digest)
	if at == 4 { appendLP(qop) }
	send(b, "digest response")
	data = recv()
	if len(data) == 0 { fmt.Println("EOF after digest"); return }
	fmt.Printf("after digest first byte=0x%02x\n", data[0])
	if data[0] == 0x14 {
		fmt.Printf("auth reply status=%d type=%d\n", data[1], data[4])
		if data[1] == 0 {
			send([]byte{0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, "kvm open")
			data = recv()
			fmt.Printf("after 0x40 first=0x%02x rest=%s ascii=%q\n", data[0], hex.EncodeToString(data[:min(64,len(data))]), string(data[:min(32,len(data))]))
		}
	}
}

func md5hex(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
func randomHex(n int) string { b := make([]byte, n); _,_ = rand.Read(b); return hex.EncodeToString(b) }
func max(a,b int) int { if a>b { return a }; return b }
func min(a,b int) int { if a<b { return a }; return b }
