//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"outband/internal/amt"
	"outband/internal/amt/redir"
)

func main() {
	host := env("OUTBAND_BMC_HOST", "192.168.8.45")
	user := env("OUTBAND_BMC_USER", "admin")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "OUTBAND_BMC_PASS required")
		os.Exit(2)
	}
	port := 16992
	if v := os.Getenv("OUTBAND_BMC_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "OUTBAND_BMC_PORT: %v\n", err)
			os.Exit(2)
		}
		port = p
	}
	useTLS := os.Getenv("OUTBAND_AMT_TLS") == "1" || strings.EqualFold(os.Getenv("OUTBAND_AMT_TLS"), "true")
	redirPort := 16994
	redirTLS := false
	if useTLS {
		redirPort = 16995
		redirTLS = true
	}
	if v := os.Getenv("OUTBAND_AMT_KVM_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "OUTBAND_AMT_KVM_PORT: %v\n", err)
			os.Exit(2)
		}
		redirPort = p
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	a := amt.New(amt.Config{Host: host, Port: port, User: user, Password: pass, TLS: useTLS})
	defer a.Close()

	info, err := a.MCInfo(ctx)
	fmt.Printf("MCInfo: err=%v info=%+v\n", err, info)
	if err != nil {
		os.Exit(1)
	}

	st, err := a.KVMStatus(ctx)
	if err != nil {
		fmt.Printf("KVMStatus: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("KVMStatus: listener=%v hasSAP=%v kvmEnabled=%d\n",
		st.ListenerEnabled, st.HasKVMSAP, st.KVMEnabledState)
	if !st.HasKVMSAP {
		fmt.Fprintln(os.Stderr, "no CIM_KVMRedirectionSAP — Hardware-KVM not available on this SKU")
		os.Exit(1)
	}

	if err := a.EnableKVM(ctx); err != nil {
		fmt.Printf("EnableKVM warn: %v\n", err)
	}

	bridge := redir.NewBridge(host, user, pass, redirPort, redirTLS, port, useTLS, nil)
	src, sink, release, err := bridge.Acquire(ctx)
	if err != nil {
		fmt.Printf("Acquire: %v\n", err)
		os.Exit(1)
	}
	defer release()
	_ = sink

	var once sync.Once
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-src.Changed():
				f := src.Frame()
				if f != nil && f.W > 0 && f.H > 0 && len(f.Pix) >= f.W*f.H*4 {
					once.Do(func() {
						fmt.Printf("frame OK: %dx%d bytes=%d\n", f.W, f.H, len(f.Pix))
						close(done)
					})
				}
			}
		}
	}()

	select {
	case <-done:
		fmt.Println("verify_amt_kvm: OK")
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "timeout waiting for first frame")
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
