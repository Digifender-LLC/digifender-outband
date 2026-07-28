//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"outband/internal/ilo"
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

	a := ilo.New(ilo.Config{
		Host:               host,
		Port:               port,
		User:               user,
		Password:           pass,
		InsecureSkipVerify: insecure,
	})
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
		ts := e.Timestamp.Format(time.RFC3339)
		if e.Timestamp.IsZero() {
			ts = "-"
		}
		fmt.Printf("  - %s %s %s\n", e.ID, ts, e.Description)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
