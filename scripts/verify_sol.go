//go:build ignore

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"mipmi/internal/ipmi"
)

func main() {
	pass := os.Getenv("MIPMI_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "MIPMI_BMC_PASS required")
		os.Exit(2)
	}
	a := ipmi.New(ipmi.Config{
		Host:     env("MIPMI_BMC_HOST", "192.168.9.74"),
		Port:     623,
		User:     env("MIPMI_BMC_USER", "root"),
		Password: pass,
		CipherID: -1,
	})
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	sess, err := a.OpenSOL(ctx)
	if err != nil {
		fmt.Printf("OpenSOL FAILED: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()
	fmt.Println("OpenSOL OK — reading up to 3s of output…")
	_, _ = sess.Write([]byte("\r"))

	done := make(chan struct{})
	total := 0
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				total += n
				_, _ = os.Stdout.Write(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					fmt.Printf("\nread err: %v\n", err)
				}
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	fmt.Printf("\nDone, read %d bytes\n", total)
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
