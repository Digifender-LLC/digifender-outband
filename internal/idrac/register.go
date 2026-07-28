package idrac

import (
	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/provider"
)

func init() {
	provider.Register("idrac", func(cfg config.HostConfig) (bmc.Client, error) {
		return New(Config{
			Host:               cfg.Host,
			Port:               cfg.Port,
			User:               cfg.User,
			Password:           cfg.Password,
			InsecureSkipVerify: cfg.IDRACInsecureSkipVerify(),
			Transport:          cfg.IDRACTransport(),
		}), nil
	})
}
