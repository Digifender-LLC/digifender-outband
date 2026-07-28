//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"outband/internal/amt"
)

func main() {
	host := os.Getenv("OUTBAND_BMC_HOST")
	user := os.Getenv("OUTBAND_BMC_USER")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	port, _ := strconv.Atoi(os.Getenv("OUTBAND_BMC_PORT"))
	a := amt.New(amt.Config{Host: host, Port: port, User: user, Password: pass})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, uri := range []string{
		"http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_KVMRedirectionSAP",
		"http://intel.com/wbem/wscim/1/amt-schema/1/AMT_RedirectionService",
		"http://intel.com/wbem/wscim/1/ips-schema/1/IPS_OptInService",
		"http://intel.com/wbem/wscim/1/ips-schema/1/IPS_KVMRedirectionSettingData",
	} {
		raw, err := a.ProbeEnumerate(ctx, uri)
		fmt.Printf("\n==== %s err=%v bytes=%d ====\n", uri[stringsLast(uri):], err, len(raw))
		if err == nil {
			s := string(raw)
			if len(s) > 4000 {
				s = s[:4000] + "…"
			}
			fmt.Println(s)
		}
	}
}

func stringsLast(uri string) int {
	for i := len(uri) - 1; i >= 0; i-- {
		if uri[i] == '/' {
			return i + 1
		}
	}
	return 0
}
