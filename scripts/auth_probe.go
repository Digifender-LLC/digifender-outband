//go:build ignore

package main

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"time"

	"outband/internal/amiweb"
)

func main() {
	host := "192.168.9.74"
	user := "root"
	pass := os.Getenv("OUTBAND_BMC_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args, cookie, err := amiweb.FetchLaunchArgs(ctx, host, user, pass)
	if err != nil {
		panic(err)
	}
	token := args["kvmtoken"]
	web := args["webcookie"]
	fmt.Printf("token=%q (%d) web=%q cookie=%q\n", token, len(token), web, cookie)
	for k, v := range args {
		fmt.Printf("  arg %s=%q\n", k, v)
	}

	mk := func(s string) []byte { h := md5.Sum([]byte(s)); return h[:] }
	pad16 := func(s string) []byte {
		b := make([]byte, 16)
		copy(b, s)
		return b
	}

	type trial struct {
		name string
		pay  []byte
		typ  byte
	}
	trials := []trial{
		{"md5(token)", mk(token), 34},
		{"md5(web)", mk(web), 34},
		{"md5(cookie)", mk(cookie), 34},
		{"token pad16", pad16(token), 34},
		{"md5(pad16 token)", func() []byte { h := md5.Sum(pad16(token)); return h[:] }(), 34},
		{"web[:16]", []byte(web)[:16], 34},
		{"md5(token+web)", mk(token + web), 34},
		{"md5(token\\0)", mk(token + "\x00"), 34},
		{"LOGIN md5(token)", mk(token), 2},
		{"md5(token) size in status", mk(token), 34},
	}

	for _, t := range trials {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "7578"), 5*time.Second)
		if err != nil {
			fmt.Println(t.name, "dial", err)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
		pkt := make([]byte, 7+len(t.pay))
		pkt[0] = t.typ
		binary.LittleEndian.PutUint32(pkt[1:5], uint32(len(t.pay)))
		binary.LittleEndian.PutUint16(pkt[5:7], 0)
		copy(pkt[7:], t.pay)
		if _, err := conn.Write(pkt); err != nil {
			fmt.Println(t.name, "write", err)
			conn.Close()
			continue
		}
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		conn.Close()
		if err != nil {
			fmt.Println(t.name, "read", err)
			continue
		}
		body := []byte{}
		if n >= 7 {
			sz := binary.LittleEndian.Uint32(buf[1:5])
			end := 7 + int(sz)
			if end > n {
				end = n
			}
			if end > 7 {
				body = buf[7:end]
			}
		}
		ok := len(body) > 0 && body[0] != 0
		fmt.Printf("%s: n=%d type=%d body=%v ok=%v\n", t.name, n, buf[0], body, ok)
		if ok {
			fmt.Println("SUCCESS", t.name)
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	amiweb.Logout(host, cookie)
}
