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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ic := ipmi.New(ipmi.Config{Host: host, Port: 623, User: user, Password: pass, CipherID: 3})
	defer ic.Close()
	raw, _ := connect(ctx, host, user, pass)
	defer raw.Close(ctx)

	args, cookie, _ := kvm.FetchLaunchArgs(ctx, host, user, pass)
	defer kvm.Logout(host, cookie)
	fr, _ := vmedia.OpenFile("./media/boot.iso")
	defer fr.Close()
	sess, _ := vmedia.Connect(ctx, vmedia.Options{Host: host, Port: vmedia.PortCD, DeviceType: vmedia.DeviceCDROM, Debug: true}, args["kvmtoken"])
	defer sess.Close()
	emu := vmedia.NewCDROM(vmedia.NewCache(fr, nil))
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go sess.Serve(mctx, emu)

	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		Persist:            true,
		BIOSBootType:       goipmi.BIOSBootTypeEFI,
		BootDeviceSelector: goipmi.BootDeviceSelectorForceCDROM,
	}
	_ = raw.SetBootParamBootFlags(ctx, flags)
	log.Println("persistent EFI CD boot set; power cycle")
	_ = ic.PowerControl(ctx, bmc.PowerCycle)

	for i := 0; i < 24; i++ {
		time.Sleep(5 * time.Second)
		b := emu.BytesServed()
		if b > 0 {
			log.Printf("SUCCESS: %d bytes read from virtual CD at t=%ds", b, (i+1)*5)
			return
		}
	}
	log.Printf("final bytes served: %d (see debug log for opcodes)", emu.BytesServed())
}

func connect(ctx context.Context, h, u, p string) (*goipmi.Client, error) {
	c, _ := goipmi.NewClient(h, 623, u, p)
	c.WithInterface(goipmi.InterfaceLanplus)
	c.WithCipherSuiteID(3)
	return c, c.Connect(ctx)
}
