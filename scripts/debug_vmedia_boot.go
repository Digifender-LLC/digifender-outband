//go:build ignore

// debug_vmedia_boot mounts with IUSB debug logging, sets CD boot, and power-cycles.
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
	"outband/internal/kvm/vmedia"
)

func main() {
	host := "192.168.9.74"
	user := "root"
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		log.Fatal("OUTBAND_BMC_PASS required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	args, cookie, err := kvm.FetchLaunchArgs(ctx, host, user, pass)
	if err != nil {
		log.Fatal(err)
	}
	defer kvm.Logout(host, cookie)

	fr, err := vmedia.OpenFile("./media/boot.iso")
	if err != nil {
		log.Fatal(err)
	}
	defer fr.Close()

	sess, err := vmedia.Connect(ctx, vmedia.Options{
		Host: host, Port: vmedia.PortCD, DeviceType: vmedia.DeviceCDROM, Debug: true,
	}, args["kvmtoken"])
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	cache := vmedia.NewCache(fr, nil)
	emu := vmedia.NewCDROM(cache)
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go func() {
		if err := sess.Serve(mctx, emu); err != nil && mctx.Err() == nil {
			log.Printf("serve ended: %v", err)
		}
	}()
	log.Printf("vmedia up; bytes served so far: 0")

	raw, _ := connectIPMI(ctx, host, user, pass)
	defer raw.Close(ctx)
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid: true, Persist: false,
		BIOSBootType: goipmi.BIOSBootTypeLegacy,
		BootDeviceSelector: goipmi.BootDeviceSelectorForceCDROM,
	}
	if err := raw.SetBootParamBootFlags(ctx, flags); err != nil {
		log.Fatal(err)
	}

	ipmiClient := ipmi.New(ipmi.Config{Host: host, Port: 623, User: user, Password: pass, CipherID: 3})
	defer ipmiClient.Close()
	log.Printf("power cycle …")
	_ = ipmiClient.PowerControl(ctx, bmc.PowerCycle)

	for i := 0; i < 12; i++ {
		time.Sleep(5 * time.Second)
		log.Printf("bytes served: %d", emu.BytesServed())
	}
	s := cache.Stats()
	log.Printf("cache: hits=%d misses=%d fetched=%d", s.Hits, s.Misses, s.FetchedBytes)
}

func connectIPMI(ctx context.Context, host, user, pass string) (*goipmi.Client, error) {
	c, err := goipmi.NewClient(host, 623, user, pass)
	if err != nil {
		return nil, err
	}
	c.WithInterface(goipmi.InterfaceLanplus)
	c.WithCipherSuiteID(3)
	return c, c.Connect(ctx)
}
