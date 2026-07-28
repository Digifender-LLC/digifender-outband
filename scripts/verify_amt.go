//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"outband/internal/amt"
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
	tls := os.Getenv("OUTBAND_AMT_TLS") == "1" || strings.EqualFold(os.Getenv("OUTBAND_AMT_TLS"), "true")

	a := amt.New(amt.Config{Host: host, Port: port, User: user, Password: pass, TLS: tls})
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
		fmt.Printf("  - %s %s %s\n", e.ID, e.Timestamp.Format(time.RFC3339), e.Description)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
