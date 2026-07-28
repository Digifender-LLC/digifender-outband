package ilo

import (
	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/provider"
)

func init() {
	provider.Register("ilo", func(cfg config.HostConfig) (bmc.Client, error) {
		return New(Config{
			Host:               cfg.Host,
			Port:               cfg.Port,
			User:               cfg.User,
			Password:           cfg.Password,
			InsecureSkipVerify: cfg.ILOInsecureSkipVerify(),
		}), nil
	})
}
