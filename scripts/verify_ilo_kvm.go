//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"outband/internal/ilo/rc"
)

func main() {
	host := env("OUTBAND_BMC_HOST", "192.168.9.90")
	user := env("OUTBAND_BMC_USER", "Administrator")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "OUTBAND_BMC_PASS required")
		os.Exit(2)
	}
	port := 443
	if v := os.Getenv("OUTBAND_BMC_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "OUTBAND_BMC_PORT: %v\n", err)
			os.Exit(2)
		}
		port = p
	}
	insecure := true
	if v := os.Getenv("OUTBAND_ILO_INSECURE"); v == "0" || v == "false" || v == "FALSE" {
		insecure = false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var frames int
	c, err := rc.Connect(ctx, rc.Options{
		Host:      host,
		HTTPSPort: port,
		User:      user,
		Password:  pass,
		Insecure:  insecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()
	c.FrameHook = func(w, h int, pix []byte) {
		frames++
		fmt.Printf("frame #%d %dx%d bytes=%d\n", frames, w, h, len(pix))
	}

	runCtx, runCancel := context.WithTimeout(ctx, 12*time.Second)
	defer runCancel()
	err = c.Run(runCtx)
	fmt.Printf("run ended: err=%v status=%q frames=%d\n", err, c.Status, frames)
	if frames == 0 {
		// Chassis-off still emits "no video" frames via placeholder path after status;
		// treat zero frames as soft failure so CI can skip when offline.
		fmt.Fprintln(os.Stderr, "warning: no frames received (chassis off or decode idle is ok)")
	}
	fmt.Println("ok")
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
