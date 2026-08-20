//go:build ignore

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
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	ic := ipmi.New(ipmi.Config{Host: host, Port: 623, User: user, Password: pass, CipherID: 3})
	defer ic.Close()
	raw, err := connect(ctx, host, user, pass)
	if err != nil {
		log.Fatal(err)
	}
	defer raw.Close(ctx)

	log.Println("power off")
	_ = ic.PowerControl(ctx, bmc.PowerOff)
	time.Sleep(8 * time.Second)

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

	emu := vmedia.NewCDROM(vmedia.NewCache(fr, nil))
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go func() {
		_ = sess.Serve(mctx, emu)
	}()
	log.Println("mounted; waiting 3s before boot override")
	time.Sleep(3 * time.Second)

	for _, trial := range []struct {
		name string
		t    goipmi.BIOSBootType
	}{
		{"legacy", goipmi.BIOSBootTypeLegacy},
		{"efi", goipmi.BIOSBootTypeEFI},
	} {
		log.Printf("try boot override %s CDROM", trial.name)
		flags := &goipmi.BootOptionParam_BootFlags{
			BootFlagsValid:     true,
			Persist:            false,
			BIOSBootType:       trial.t,
			BootDeviceSelector: goipmi.BootDeviceSelectorForceCDROM,
		}
		if err := raw.SetBootParamBootFlags(ctx, flags); err != nil {
			log.Printf("boot flags: %v", err)
			continue
		}
		log.Println("power on")
		_ = ic.PowerControl(ctx, bmc.PowerOn)
		for i := 0; i < 18; i++ {
			time.Sleep(5 * time.Second)
			b := emu.BytesServed()
			log.Printf("[%s] t=%ds bytes=%d", trial.name, (i+1)*5, b)
			if b > 0 {
				log.Printf("SUCCESS: host read %d bytes from virtual CD", b)
				return
			}
		}
		log.Println("no reads; power off for next attempt")
		_ = ic.PowerControl(ctx, bmc.PowerOff)
		time.Sleep(8 * time.Second)
	}
	log.Println("host did not read virtual CD in either legacy or EFI mode")
}

func connect(ctx context.Context, h, u, p string) (*goipmi.Client, error) {
	c, err := goipmi.NewClient(h, 623, u, p)
	if err != nil {
		return nil, err
	}
	c.WithInterface(goipmi.InterfaceLanplus)
	c.WithCipherSuiteID(3)
	return c, c.Connect(ctx)
}
