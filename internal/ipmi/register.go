package ipmi

import (
	"mipmi/internal/bmc"
	"mipmi/internal/config"
	"mipmi/internal/provider"
)

func init() {
	provider.Register("ipmi", func(cfg config.HostConfig) (bmc.Client, error) {
		return New(Config{
			Host:     cfg.Host,
			Port:     cfg.Port,
			User:     cfg.User,
			Password: cfg.Password,
			CipherID: cfg.CipherID(),
		}), nil
	})
}
