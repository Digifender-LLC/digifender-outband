//go:build ignore

// verify_vmedia_boot mounts a bootable ISO, sets one-time CD-ROM boot via IPMI,
// and power-cycles the host. Keeps virtual media up while the host reads the disc.
//
// Usage:
//
//	export OUTBAND_BMC_PASS=superuser
//	go run scripts/verify_vmedia_boot.go [-iso ./media/boot.iso]
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	goipmi "github.com/bougou/go-ipmi"

	"outband/internal/bmc"
	"outband/internal/ipmi"
	"outband/internal/kvm"
)

func main() {
	host := env("OUTBAND_BMC_HOST", "192.168.9.74")
	user := env("OUTBAND_BMC_USER", "root")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	iso := flag.String("iso", "./media/boot.iso", "bootable ISO path")
	wait := flag.Duration("wait", 90*time.Second, "how long to keep media mounted after power cycle")
	flag.Parse()

	if pass == "" {
		log.Fatal("OUTBAND_BMC_PASS required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *wait+2*time.Minute)
	defer cancel()

	ipmiClient := ipmi.New(ipmi.Config{
		Host: host, Port: 623, User: user, Password: pass, CipherID: 3,
	})
	defer ipmiClient.Close()

	raw, err := connectIPMI(ctx, host, user, pass)
	if err != nil {
		log.Fatalf("ipmi connect: %v", err)
	}
	defer raw.Close(ctx)

	ps, err := ipmiClient.PowerStatus(ctx)
	if err != nil {
		log.Fatalf("power status: %v", err)
	}
	log.Printf("initial power: on=%v", ps.IsOn)

	selBefore, _ := ipmiClient.SEL(ctx, 3)
	log.Printf("recent SEL before test (%d entries)", len(selBefore))

	mgr := kvm.NewMediaManager(host, user, pass, "", time.Hour, nil)
	log.Printf("mounting %s …", *iso)
	if err := mgr.Mount(ctx, *iso); err != nil {
		log.Fatalf("mount: %v", err)
	}
	defer mgr.Unmount()
	st := mgr.Status()
	log.Printf("virtual CD mounted: %s (%d bytes)", st.ISO, st.Size)

	if err := setBootCDROMOnce(ctx, raw); err != nil {
		log.Fatalf("set boot device: %v", err)
	}
	log.Printf("IPMI boot override: force CD-ROM (next boot only)")

	action := bmc.PowerCycle
	if !ps.IsOn {
		action = bmc.PowerOn
	}
	if err := ipmiClient.PowerControl(ctx, action); err != nil {
		log.Fatalf("power %s: %v", action, err)
	}
	log.Printf("issued power %s", action)

	deadline := time.Now().Add(*wait)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		ps, err := ipmiClient.PowerStatus(ctx)
		if err != nil {
			log.Printf("power poll: %v", err)
			continue
		}
		log.Printf("power: on=%v", ps.IsOn)
		sel, err := ipmiClient.SEL(ctx, 5)
		if err == nil && len(sel) > 0 {
			e := sel[0]
			log.Printf("latest SEL: %s %s", e.Timestamp.Format(time.RFC3339), e.Description)
		}
	}

	log.Printf("clearing boot override")
	_ = clearBootOverride(ctx, raw)
	log.Printf("done — check KVM/console for El Torito banner if the host booted the virtual CD")
}

func connectIPMI(ctx context.Context, host, user, pass string) (*goipmi.Client, error) {
	c, err := goipmi.NewClient(host, 623, user, pass)
	if err != nil {
		return nil, err
	}
	c.WithInterface(goipmi.InterfaceLanplus)
	c.WithCipherSuiteID(3)
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func setBootCDROMOnce(ctx context.Context, c *goipmi.Client) error {
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		Persist:            false,
		BIOSBootType:       goipmi.BIOSBootTypeLegacy,
		BootDeviceSelector: goipmi.BootDeviceSelectorForceCDROM,
	}
	return c.SetBootParamBootFlags(ctx, flags)
}

func clearBootOverride(ctx context.Context, c *goipmi.Client) error {
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		BootDeviceSelector: goipmi.BootDeviceSelectorNoOverride,
	}
	return c.SetBootParamBootFlags(ctx, flags)
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
