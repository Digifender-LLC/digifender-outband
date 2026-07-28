//go:build ignore
package main

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"outband/internal/amiweb"
)

func readPkt(conn net.Conn) (byte, uint16, []byte, error) {
	h := make([]byte, 7)
	if _, err := io.ReadFull(conn, h); err != nil {
		return 0, 0, nil, err
	}
	typ := h[0]
	sz := binary.LittleEndian.Uint32(h[1:5])
	st := binary.LittleEndian.Uint16(h[5:7])
	body := make([]byte, sz)
	if sz > 0 {
		if _, err := io.ReadFull(conn, body); err != nil {
			return typ, st, nil, err
		}
	}
	return typ, st, body, nil
}

func send(conn net.Conn, typ byte, body []byte) error {
	pkt := make([]byte, 7+len(body))
	pkt[0] = typ
	binary.LittleEndian.PutUint32(pkt[1:5], uint32(len(body)))
	copy(pkt[7:], body)
	_, err := conn.Write(pkt)
	return err
}

func main() {
	host := "192.168.9.74"
	pass := os.Getenv("OUTBAND_BMC_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args, cookie, err := amiweb.FetchLaunchArgs(ctx, host, "root", pass)
	if err != nil {
		panic(err)
	}
	token := args["kvmtoken"]
	fmt.Println("token", token)
	defer amiweb.Logout(host, cookie)

	conn, err := net.DialTimeout("tcp", host+":7578", 5*time.Second)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))

	// 1) GET_CHALLENGE
	_ = send(conn, 1, nil)
	typ, st, body, err := readPkt(conn)
	fmt.Printf("after challenge: type=%d status=%d body=%x err=%v\n", typ, st, body, err)
	if err != nil {
		return
	}

	user := []byte("root")
	ch := body
	if len(ch) > 32 {
		ch = ch[:32]
	}

	for _, t := range []struct {
		name string
		pay  []byte
	}{
		{"md5(token)", func() []byte { h := md5.Sum([]byte(token)); return h[:] }()},
		{"md5(token+ch)", func() []byte { h := md5.Sum(append([]byte(token), ch...)); return h[:] }()},
		{"md5(ch+token)", func() []byte { h := md5.Sum(append(append([]byte{}, ch...), token...)); return h[:] }()},
	} {
		c2, err := net.DialTimeout("tcp", host+":7578", 5*time.Second)
		if err != nil {
			fmt.Println(t.name, "dial", err)
			break
		}
		_ = c2.SetDeadline(time.Now().Add(6 * time.Second))
		_ = send(c2, 34, t.pay)
		typ, st, body, err := readPkt(c2)
		c2.Close()
		ok := len(body) > 0 && body[0] != 0
		fmt.Printf("%s -> type=%d st=%d body=%v ok=%v err=%v\n", t.name, typ, st, body, ok, err)
		if ok {
			fmt.Println("SUCCESS")
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	// LOGIN with username+digest after challenge on fresh conn
	c3, err := net.DialTimeout("tcp", host+":7578", 5*time.Second)
	if err != nil {
		panic(err)
	}
	defer c3.Close()
	_ = c3.SetDeadline(time.Now().Add(10 * time.Second))
	_ = send(c3, 1, nil)
	typ, st, body, err = readPkt(c3)
	fmt.Printf("ch2 type=%d body=%x\n", typ, body)
	if err == nil && len(body) > 0 {
		// classic AMI: MD5(username + challenge + password) or similar
		passb := []byte(pass)
		for _, name := range []string{"u+c+p", "p+c", "u+p+c"} {
			var h [16]byte
			switch name {
			case "u+c+p":
				h = md5.Sum(append(append(user, body...), passb...))
			case "p+c":
				h = md5.Sum(append(passb, body...))
			case "u+p+c":
				h = md5.Sum(append(append(user, passb...), body...))
			}
			pay := make([]byte, 32+16)
			copy(pay, user)
			copy(pay[32:], h[:])
			_ = send(c3, 2, pay)
			typ, st, body, err = readPkt(c3)
			fmt.Printf("login %s -> type=%d st=%d body=%v err=%v\n", name, typ, st, body, err)
			if err != nil {
				break
			}
		}
	}
}
