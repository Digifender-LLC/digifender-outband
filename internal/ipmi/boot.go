package ipmi

import (
	"context"
	"fmt"

	goipmi "github.com/bougou/go-ipmi"
)

// SetBootCDROMOnce sets a one-time force CD-ROM boot override via IPMI boot flags.
func (a *Adapter) SetBootCDROMOnce(ctx context.Context) error {
	c, err := a.ensureClient(ctx)
	if err != nil {
		return err
	}
	flags := &goipmi.BootOptionParam_BootFlags{
		BootFlagsValid:     true,
		Persist:            false,
		BIOSBootType:       goipmi.BIOSBootTypeLegacy,
		BootDeviceSelector: goipmi.BootDeviceSelectorForceCDROM,
	}
	if err := c.SetBootParamBootFlags(ctx, flags); err != nil {
		return fmt.Errorf("ipmi: set boot device CD-ROM: %w", err)
	}
	return nil
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
