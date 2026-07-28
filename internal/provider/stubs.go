package provider

import (
	"fmt"

	"mipmi/internal/bmc"
	"mipmi/internal/config"
)

func init() {
	Register("idrac", stubFactory("idrac"))
	Register("amt", stubFactory("amt"))
}

func stubFactory(name string) Factory {
	return func(cfg config.HostConfig) (bmc.Client, error) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotImplemented)
	}
}
