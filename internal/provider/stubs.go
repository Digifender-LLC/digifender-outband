package provider

import (
	"fmt"

	"outband/internal/bmc"
	"outband/internal/config"
)

func init() {
	Register("idrac", stubFactory("idrac"))
}

func stubFactory(name string) Factory {
	return func(cfg config.HostConfig) (bmc.Client, error) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotImplemented)
	}
}
