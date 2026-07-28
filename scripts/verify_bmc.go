//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"mipmi/internal/ipmi"
)

func main() {
	host := env("MIPMI_BMC_HOST", "192.168.9.74")
	user := env("MIPMI_BMC_USER", "root")
	pass := os.Getenv("MIPMI_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "MIPMI_BMC_PASS required")
		os.Exit(2)
	}
	a := ipmi.New(ipmi.Config{Host: host, Port: 623, User: user, Password: pass, CipherID: -1})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	info, err := a.MCInfo(ctx)
	fmt.Printf("MCInfo: err=%v info=%+v\n", err, info)

	ps, err := a.PowerStatus(ctx)
	fmt.Printf("Power: err=%v status=%+v\n", err, ps)

	sensors, err := a.Sensors(ctx)
	fmt.Printf("Sensors: err=%v count=%d\n", err, len(sensors))
	for i, s := range sensors {
		if i >= 5 {
			break
		}
		fmt.Printf("  - %s = %s %s (%s)\n", s.Name, s.Value, s.Unit, s.Status)
	}

	sel, err := a.SEL(ctx, 5)
	fmt.Printf("SEL: err=%v count=%d\n", err, len(sel))
	for _, e := range sel {
		fmt.Printf("  - %04x %s %s\n", e.ID, e.Timestamp.Format(time.RFC3339), e.Description)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
