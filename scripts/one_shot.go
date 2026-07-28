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

	"mipmi/internal/amiweb"
)

func main() {
	host := "192.168.9.74"
	pass := os.Getenv("MIPMI_BMC_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	args, cookie, err := amiweb.FetchLaunchArgs(ctx, host, "root", pass)
	if err != nil {
		panic(err)
	}
	token := args["kvmtoken"]
	fmt.Printf("token=%q cookie=%q\n", token, cookie)

	conn, err := net.DialTimeout("tcp", host+":7578", 5*time.Second)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	sum := md5.Sum([]byte(token))
	pkt := make([]byte, 23)
	pkt[0] = 34
	binary.LittleEndian.PutUint32(pkt[1:5], 16)
	copy(pkt[7:], sum[:])
	fmt.Printf("sending %x\n", pkt)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = conn.Write(pkt)

	buf := make([]byte, 8)
	n, err := io.ReadFull(conn, buf)
	fmt.Printf("read n=%d err=%v hex=%x\n", n, err, buf[:n])
	if n >= 8 {
		typ := buf[0]
		sz := binary.LittleEndian.Uint32(buf[1:5])
		st := binary.LittleEndian.Uint16(buf[5:7])
		body := make([]byte, sz)
		if sz > 0 {
			_, _ = io.ReadFull(conn, body)
		}
		fmt.Printf("type=%d size=%d status=%d body=%v\n", typ, sz, st, body)

		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		resume := []byte{14, 0, 0, 0, 0, 0, 0}
		_, _ = conn.Write(resume)
		bw := make([]byte, 11)
		bw[0] = 7
		binary.LittleEndian.PutUint32(bw[1:5], 4)
		binary.LittleEndian.PutUint32(bw[7:11], 13107200)
		_, _ = conn.Write(bw)

		total := 0
		types := map[byte]int{}
		tmp := make([]byte, 65536)
		for {
			h := make([]byte, 7)
			if _, err := io.ReadFull(conn, h); err != nil {
				fmt.Println("stream end", err, "total", total, "types", types)
				break
			}
			t := h[0]
			psz := binary.LittleEndian.Uint32(h[1:5])
			types[t]++
			total++
			if psz > 0 {
				if int(psz) > len(tmp) {
					tmp = make([]byte, psz)
				}
				_, _ = io.ReadFull(conn, tmp[:psz])
			}
			if total <= 8 || t == 5 {
				fmt.Printf("pkt type=%d size=%d\n", t, psz)
			}
			if total > 40 {
				fmt.Println("cap types", types)
				break
			}
		}
	}
	amiweb.Logout(host, cookie)
}
