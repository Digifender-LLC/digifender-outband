//go:build ignore

// Simulates UI mount: parent context cancelled when the HTTP handler returns.
// After attachVMedia fix the vmedia session must stay up and the host should read.
package main

import (
	"context"
	"log"
	"os"
	"time"

	goipmi "github.com/bougou/go-ipmi"

	"outband/internal/bmc"
	"outband/internal/ipmi"
	"outband/internal/kvm"
)

func main() {
	host := "192.168.9.74"
	user := "root"
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		log.Fatal("OUTBAND_BMC_PASS required")
	}

	ic := ipmi.New(ipmi.Config{Host: host, Port: 623, User: user, Password: pass, CipherID: 3})
	defer ic.Close()

	reqCtx, cancelReq := context.WithCancel(context.Background())
	mgr := kvm.NewMediaManager(host, user, pass, "./media", time.Hour, nil)
	if err := mgr.MountRequest(reqCtx, kvm.MountRequest{
		Source:  kvm.MountSourceLibrary,
		Library: "boot.iso",
	}); err != nil {
		log.Fatal(err)
	}
	log.Println("mounted; cancelling request context (simulates HTTP response)")
	cancelReq()

	raw, _ := goipmi.NewClient(host, 623, user, pass)
	raw.WithInterface(goipmi.InterfaceLanplus)
	raw.WithCipherSuiteID(3)
	bootCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = raw.Connect(bootCtx)
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:         true,
		BIOSBootType:           goipmi.BIOSBootTypeLegacy,
		BootDeviceSelector:     goipmi.BootDeviceSelectorForceRemoteCDROM,
		DeviceInstanceSelector: 0,
	}
	_ = raw.SetBootParamBootFlags(bootCtx, flags)
	_ = ic.PowerControl(bootCtx, bmc.PowerCycle)

	for i := 0; i < 30; i++ {
		time.Sleep(3 * time.Second)
		st := mgr.Status()
		log.Printf("t=%ds bytes=%d mounted=%v", (i+1)*3, st.BytesServed, st.Mounted)
		if st.BytesServed > 0 {
			log.Printf("SUCCESS: session survived request cancel; host read %d bytes", st.BytesServed)
			return
		}
	}
	log.Printf("FAIL: 0 bytes after request cancel (mounted=%v)", mgr.Status().Mounted)
}
