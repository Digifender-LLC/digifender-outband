package provider

import (
	"fmt"

	"outband/internal/bmc"
	"outband/internal/config"
)

func init() {
	// Generic stub for hosts.Open skip-path tests and docs — not a shipping BMC.
	Register("unimplemented", stubFactory("unimplemented"))
}

func stubFactory(name string) Factory {
	return func(cfg config.HostConfig) (bmc.Client, error) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotImplemented)
	}
}
