//go:build ignore

package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"time"

	"outband/internal/amiweb"
)

func main() {
	ctx := context.Background()
	host := env("OUTBAND_BMC_HOST", "192.168.9.74")
	user := env("OUTBAND_BMC_USER", "root")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "OUTBAND_BMC_PASS required")
		os.Exit(2)
	}
	sess, err := amiweb.Login(ctx, host, user, pass)
	if err != nil {
		panic(err)
	}
	defer amiweb.Logout(host, sess.Cookie)
	fmt.Printf("token=%q cookie=%q web=%q\n", sess.KVMToken, sess.Cookie, sess.WebCookie)

	variants := []struct {
		name   string
		token  string
		withCC bool
		ttype  byte
	}{
		{"kvmtoken+CC", sess.KVMToken, true, 0},
		{"kvmtoken", sess.KVMToken, false, 0},
		{"webcookie+CC", sess.WebCookie, true, 0},
		{"webcookie", sess.WebCookie, false, 0},
		{"cookie+CC", sess.Cookie, true, 0},
		{"kvmtoken+CC t1", sess.KVMToken, true, 1},
	}
	for _, v := range variants {
		fmt.Println("===", v.name, "===")
		conn, err := net.DialTimeout("tcp", netJoin(host, 7578), 5*time.Second)
		if err != nil {
			fmt.Println("dial", err)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		if v.withCC {
			hdr := make([]byte, 8)
			binary.LittleEndian.PutUint16(hdr[0:], 58)
			_, _ = conn.Write(hdr)
		}
		pkt := make([]byte, 8+373)
		binary.LittleEndian.PutUint16(pkt[0:], 18)
		binary.LittleEndian.PutUint32(pkt[2:], 373)
		body := pkt[8:]
		body[v.ttype] = v.ttype // oops fix below
		body[0] = v.ttype
		copy(body[1:], v.token)
		copy(body[130:], "10.8.0.4")
		copy(body[195:], "root")
		copy(body[324:], "aa-bb-cc-dd-ee-ff")
		_, _ = conn.Write(pkt)
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		fmt.Printf("read n=%d err=%v hex=%s\n", n, err, hex.EncodeToString(buf[:max(0, n)]))
		_ = conn.Close()
		time.Sleep(400 * time.Millisecond)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func netJoin(h string, p int) string { return fmt.Sprintf("%s:%d", h, p) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
