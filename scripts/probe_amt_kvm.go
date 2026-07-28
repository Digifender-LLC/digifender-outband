//go:build ignore

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
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

	a := amt.New(amt.Config{Host: host, Port: port, User: user, Password: pass, TLS: useTLS})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	info, err := a.MCInfo(ctx)
	fmt.Printf("MCInfo: err=%v info=%+v\n", err, info)

	st, err := a.KVMStatus(ctx)
	if err != nil {
		fmt.Printf("KVMStatus: %v\n", err)
	} else {
		fmt.Printf("KVMStatus: listener=%v enabledState=%d hasSAP=%v kvmEnabled=%d kvmRequested=%d\n",
			st.ListenerEnabled, st.EnabledState, st.HasKVMSAP, st.KVMEnabledState, st.KVMRequested)
		for k, v := range st.SettingFields {
			fmt.Printf("  setting %s=%s\n", k, v)
		}
	}

	fmt.Println("=== ports before enable ===")
	checkPorts(host)

	if err := a.EnableKVM(ctx); err != nil {
		fmt.Printf("EnableKVM: %v\n", err)
	} else {
		fmt.Println("EnableKVM: OK")
	}
	time.Sleep(2 * time.Second)

	fmt.Println("=== ports after enable ===")
	checkPorts(host)

	st2, _ := a.KVMStatus(ctx)
	if st2 != nil {
		fmt.Printf("KVMStatus after: listener=%v enabledState=%d kvmEnabled=%d\n",
			st2.ListenerEnabled, st2.EnabledState, st2.KVMEnabledState)
	}

	fmt.Println("=== redirection dial ===")
	conn, err := redir.Dial(redir.Options{Host: host, Port: 16994, User: user, Password: pass})
	if err != nil {
		fmt.Printf("Dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("Dial+auth OK; reading RFB version…")
	buf := make([]byte, 12)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	fmt.Printf("RFB peek n=%d err=%v data=%q\n", n, err, string(buf[:max(0, n)]))
}

func checkPorts(host string) {
	for _, p := range []int{16992, 16993, 16994, 16995, 5900} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(p)), 3*time.Second)
		if err != nil {
			fmt.Printf("port %d: %v\n", p, err)
			continue
		}
		_ = c.Close()
		fmt.Printf("port %d: OPEN\n", p)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
