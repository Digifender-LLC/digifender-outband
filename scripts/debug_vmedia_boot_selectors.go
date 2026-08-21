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

	trials := []struct {
		name string
		sel  goipmi.BootDeviceSelector
		bt   goipmi.BIOSBootType
	}{
		{"remote-cd-efi", goipmi.BootDeviceSelectorForceRemoteCDROM, goipmi.BIOSBootTypeEFI},
		{"remote-cd-legacy", goipmi.BootDeviceSelectorForceRemoteCDROM, goipmi.BIOSBootTypeLegacy},
		{"remote-media-efi", goipmi.BootDeviceSelectorForceRemoteMedia, goipmi.BIOSBootTypeEFI},
		{"cd-efi", goipmi.BootDeviceSelectorForceCDROM, goipmi.BIOSBootTypeEFI},
	}

	for _, tr := range trials {
		log.Printf("=== trial %s ===", tr.name)
		_ = ic.PowerControl(ctx, bmc.PowerOff)
		time.Sleep(8 * time.Second)

		args, cookie, err := kvm.FetchLaunchArgs(ctx, host, user, pass)
		if err != nil {
			log.Fatal(err)
		}

		fr, err := vmedia.OpenFile("./media/boot.iso")
		if err != nil {
			kvm.Logout(host, cookie)
			log.Fatal(err)
		}

		sess, err := vmedia.Connect(ctx, vmedia.Options{
			Host: host, Port: vmedia.PortCD, DeviceType: vmedia.DeviceCDROM,
		}, args["kvmtoken"])
		if err != nil {
			fr.Close()
			kvm.Logout(host, cookie)
			log.Fatal(err)
		}

		emu := vmedia.NewCDROM(vmedia.NewCache(fr, nil))
		mctx, mcancel := context.WithCancel(ctx)
		go func() { _ = sess.Serve(mctx, emu) }()
		time.Sleep(2 * time.Second)

		flags := &goipmi.BootOptionParam_BootFlags{
			BootFlagsValid:     true,
			Persist:            false,
			BIOSBootType:       tr.bt,
			BootDeviceSelector: tr.sel,
		}
		if err := raw.SetBootParamBootFlags(ctx, flags); err != nil {
			log.Printf("boot flags: %v", err)
			cleanup(mcancel, sess, fr, host, cookie)
			continue
		}

		_ = ic.PowerControl(ctx, bmc.PowerOn)
		for i := 0; i < 20; i++ {
			time.Sleep(3 * time.Second)
			if b := emu.BytesServed(); b > 0 {
				log.Printf("SUCCESS %s: %d bytes at t=%ds", tr.name, b, (i+1)*3)
				cleanup(mcancel, sess, fr, host, cookie)
				return
			}
		}
		log.Printf("FAIL %s: 0 bytes", tr.name)
		cleanup(mcancel, sess, fr, host, cookie)
		time.Sleep(5 * time.Second)
	}
	log.Println("no selector produced host reads")
}

func cleanup(cancel context.CancelFunc, sess *vmedia.Session, fr *vmedia.FileReader, host, cookie string) {
	cancel()
	_ = sess.Close()
	_ = fr.Close()
	kvm.Logout(host, cookie)
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
