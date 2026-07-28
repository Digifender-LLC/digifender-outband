//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"mipmi/internal/amiweb"
	"mipmi/internal/kvm"
	"mipmi/internal/kvm/codec"
)

func main() {
	host := env("MIPMI_BMC_HOST", "192.168.9.74")
	user := env("MIPMI_BMC_USER", "root")
	pass := os.Getenv("MIPMI_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "MIPMI_BMC_PASS required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	sess, err := amiweb.Login(ctx, host, user, pass)
	if err != nil {
		fmt.Println("amiweb.Login FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("amiweb OK token=%dB cookie=%dB port=%d\n", len(sess.KVMToken), len(sess.Cookie), sess.Port)
	amiweb.Logout(host, sess.Cookie)

	frames := 0
	c, err := kvm.Connect(ctx, kvm.Options{Host: host, Port: 7578, TLS: false, User: user}, pass)
	if err != nil {
		fmt.Println("kvm.Connect FAILED:", err)
		os.Exit(1)
	}
	c.OnFrame = func(f *codec.Frame) {
		frames++
		if frames <= 3 || frames%30 == 0 {
			fmt.Printf("frame %d %dx%d pix=%d\n", frames, f.W, f.H, len(f.Pix))
		}
	}
	go func() {
		time.Sleep(8 * time.Second)
		cancel()
	}()
	err = c.Run(ctx)
	fmt.Printf("Run ended err=%v frames=%d\n", err, frames)
	if frames == 0 {
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
