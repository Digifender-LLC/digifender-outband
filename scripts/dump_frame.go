//go:build ignore

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"mipmi/internal/kvm"
	"mipmi/internal/kvm/codec"
)

func main() {
	host := "192.168.9.74"
	pass := os.Getenv("MIPMI_BMC_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := kvm.Connect(ctx, kvm.Options{Host: host, Port: 7578, User: "root"}, pass)
	if err != nil {
		panic(err)
	}
	// Hook before decode by temporarily replacing - use raw via OnFrame won't work.
	// Instead dump from a custom client path: monkey via reading - dump in handleFrame by
	// wrapping decoder. Simpler: call Connect and use internal dump script via modified OnFrame
	// that we can't access raw for. Use codec after fixing - for now print via interceptor.

	rawFrames := 0
	c.OnFrame = func(f *codec.Frame) {
		rawFrames++
		fmt.Printf("decoded %dx%d\n", f.W, f.H)
	}
	// We need raw bytes - add temporary dump by re-running with a local decode of first frame
	// Use the client's internal path by patching verify to dump. For now run Run and hope.
	_ = hex.Dumper
	go func() {
		time.Sleep(6 * time.Second)
		cancel()
	}()
	err = c.Run(ctx)
	fmt.Println("done", err, "frames", rawFrames)
}
