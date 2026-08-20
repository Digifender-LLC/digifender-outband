package kvm

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"outband/internal/kvm/vmedia"
)

// vmediaPort maps a device kind to its BMC port, preferring the jnlp value and
// falling back to the documented defaults.
func vmediaPort(kind string, args map[string]string) (int, error) {
	switch kind {
	case "cd":
		return portOr(args["cdport"], vmedia.PortCD), nil
	case "fd":
		return portOr(args["fdport"], vmedia.PortFD), nil
	case "hd":
		return portOr(args["hdport"], vmedia.PortHD), nil
	default:
		return 0, fmt.Errorf("vmedia: unknown device kind %q", kind)
	}
}

func portOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

// buildCDEmulator fronts an ISO backing with a windowed read cache and CD-ROM SCSI profile.
func buildCDEmulator(backing vmedia.Reader) (*vmedia.Device, *vmedia.Cache) {
	cache := vmedia.NewCache(backing, nil)
	return vmedia.NewCDROM(cache), cache
}

// attachVMedia opens the device's vmedia port using a web session token and runs the
// SCSI emulation loop against backing until ctx is cancelled.
// stats reports bytes served to the host (READ data); nil when attach fails.
func attachVMedia(ctx context.Context, host string, kind string, token string, args map[string]string, backing vmedia.Reader) (stop func(), stats func() int64, err error) {
	port, err := vmediaPort(kind, args)
	if err != nil {
		return nil, nil, err
	}
	sess, err := vmedia.Connect(ctx, vmedia.Options{
		Host:       host,
		Port:       port,
		DeviceType: vmedia.DeviceCDROM,
		Instance:   0,
		TLS:        args["vmsecure"] == "1",
	}, token)
	if err != nil {
		return nil, nil, err
	}

	emu, cache := buildCDEmulator(backing)
	mctx, mcancel := context.WithCancel(ctx)
	go func() {
		if err := sess.Serve(mctx, emu); err != nil && mctx.Err() == nil {
			log.Printf("vmedia: %s session ended: %v", kind, err)
		}
	}()

	stop = func() {
		mcancel()
		_ = sess.Close()
		if cache != nil {
			s := cache.Stats()
			if total := s.Hits + s.Misses; total > 0 {
				log.Printf("vmedia: %s cache — %d hits / %d misses (%.0f%%), %d KiB fetched",
					kind, s.Hits, s.Misses, 100*float64(s.Hits)/float64(total), s.FetchedBytes/1024)
			}
		}
		log.Printf("vmedia: %s served %d bytes to host", kind, emu.BytesServed())
	}
	statsFn := func() int64 { return emu.BytesServed() }
	return stop, statsFn, nil
}
