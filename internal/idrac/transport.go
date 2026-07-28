package idrac

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"outband/internal/bmc"
)

// Transport names accepted in inventory / Config.Transport.
const (
	TransportAuto    = "auto"
	TransportRedfish = "redfish"
	TransportWSMAN   = "wsman"
	TransportWeb     = "web"
)

func normalizeTransport(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", TransportAuto:
		return TransportAuto
	case TransportRedfish, TransportWSMAN, TransportWeb:
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return TransportAuto
	}
}

// backend is the per-host wire protocol. Each inventory host gets its own Adapter
// and therefore its own backend — mixed iDRAC generations in one process are fine.
type backend interface {
	Name() string
	MCInfo(ctx context.Context) (*bmc.MCInfo, error)
	PowerStatus(ctx context.Context) (*bmc.PowerStatus, error)
	PowerControl(ctx context.Context, action bmc.PowerAction) error
	Sensors(ctx context.Context) ([]bmc.Sensor, error)
	SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error)
	Close() error
}

func baseURL(cfg Config) string {
	port := cfg.Port
	if port == 0 {
		port = 443
	}
	return fmt.Sprintf("https://%s:%d", cfg.Host, port)
}

// modernTLS is suitable for iDRAC8/9/10 and current OpenSSL defaults.
func modernTLS(insecure bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure, //nolint:gosec // BMC self-signed certs are common
	}
}

// legacyTLS enables TLS 1.0 / RSA key-exchange ciphers used by iDRAC6/7.
func legacyTLS(insecure bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure, //nolint:gosec // iDRAC7 default certs are self-signed + expired
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		},
	}
}

func newHTTPClient(tlsCfg *tls.Config, withCookies bool) *http.Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		TLSClientConfig:     tlsCfg,
		MaxIdleConns:        2,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  false,
	}
	c := &http.Client{
		Transport: tr,
		Timeout:   45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	if withCookies {
		jar, err := cookiejar.New(nil)
		if err == nil {
			c.Jar = jar
		}
	}
	return c
}

// probeClient tries modern TLS first, then legacy — used only for detection.
func probeHTTPClient(cfg Config) (client *http.Client, legacy bool, err error) {
	modern := newHTTPClient(modernTLS(cfg.InsecureSkipVerify), false)
	if err := probeTLS(modern, baseURL(cfg)); err == nil {
		return modern, false, nil
	}
	leg := newHTTPClient(legacyTLS(cfg.InsecureSkipVerify), false)
	if err := probeTLS(leg, baseURL(cfg)); err != nil {
		return nil, false, fmt.Errorf("idrac tls: modern and legacy handshakes failed: %w", err)
	}
	return leg, true, nil
}

func probeTLS(c *http.Client, root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
