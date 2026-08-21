package ipmi

import (
	"context"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// SetBootCDROMOnce sets a one-time force CD-ROM boot override (legacy BIOS).
func (a *Adapter) SetBootCDROMOnce(ctx context.Context) error {
	return a.setBootVirtualCDOnce(ctx, goipmi.BIOSBootTypeLegacy)
}

// SetBootCDROMOnceEFI sets a one-time force CD-ROM boot override (UEFI).
func (a *Adapter) SetBootCDROMOnceEFI(ctx context.Context) error {
	return a.setBootVirtualCDOnce(ctx, goipmi.BIOSBootTypeEFI)
}

func (a *Adapter) setBootVirtualCDOnce(ctx context.Context, bootType goipmi.BIOSBootType) error {
	c, err := a.ensureClient(ctx)
	if err != nil {
		return err
	}
	// AMI virtual CD is redirected over IUSB — prefer the remote-media selectors.
	selectors := []goipmi.BootDeviceSelector{
		goipmi.BootDeviceSelectorForceRemoteCDROM,
		goipmi.BootDeviceSelectorForceRemoteMedia,
		goipmi.BootDeviceSelectorForceCDROM,
	}
	var lastErr error
	for _, sel := range selectors {
		flags := &goipmi.BootOptionParam_BootFlags{
			BootFlagsValid:         true,
			Persist:                false,
			BIOSBootType:           bootType,
			BootDeviceSelector:     sel,
			DeviceInstanceSelector: 0,
		}
		if err := c.SetBootParamBootFlags(ctx, flags); err != nil {
			lastErr = fmt.Errorf("ipmi: set boot device %s: %w", sel, err)
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("ipmi: set boot device CD-ROM: no selectors accepted")
}

// ClearBootOverride clears the IPMI boot device override.
func (a *Adapter) ClearBootOverride(ctx context.Context) error {
	c, err := a.ensureClient(ctx)
	if err != nil {
		return err
	}
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		BootDeviceSelector: goipmi.BootDeviceSelectorNoOverride,
	}
	if err := c.SetBootParamBootFlags(ctx, flags); err != nil {
		return fmt.Errorf("ipmi: clear boot override: %w", err)
	}
	return nil
}
