package rc

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RcInfo is the parsed /json/rc_info payload.
type RcInfo struct {
	EncKeyHex        string
	EncKey           []byte
	RCPort           int
	VMPort           int
	OptionalFeatures string
	ProtocolVersion  string
	ServerName       string
	ILOFQDN          string
	EncryptKey       bool
	EncryptVMKey     bool
}

// Session is an authenticated HTTPS session against one iLO.
type Session struct {
	Host       string
	Port       int
	Insecure   bool
	SessionKey string
	client     *http.Client
}

// Login mints a session_key via POST /json/login_session.
func Login(host string, port int, user, pass string, insecure bool) (*Session, error) {
	if port == 0 {
		port = 443
	}
	s := &Session{
		Host:     host,
		Port:     port,
		Insecure: insecure,
		client: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
			},
		},
	}
	body, err := json.Marshal(map[string]string{
		"method": "login", "user_login": user, "password": pass,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, s.base()+"/json/login_session", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ilo login: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ilo login: HTTP %d: %s", res.StatusCode, truncate(raw))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("ilo login: %w", err)
	}
	sk, _ := m["session_key"].(string)
	if sk == "" {
		sk, _ = m["sessionKey"].(string)
	}
	if sk == "" {
		return nil, fmt.Errorf("ilo login: no session_key")
	}
	if priv, ok := m["remote_cons_priv"]; ok && !truthy(priv) {
		return nil, fmt.Errorf("ilo login: remote console privilege denied")
	}
	s.SessionKey = sk
	return s, nil
}

// Close logs out the web session (best-effort).
func (s *Session) Close() {
	if s == nil || s.SessionKey == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"method": "logout", "session_key": s.SessionKey,
	})
	req, err := http.NewRequest(http.MethodPost, s.base()+"/json/login_session", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "sessionKey="+s.SessionKey)
	res, err := s.client.Do(req)
	if err == nil {
		res.Body.Close()
	}
	s.SessionKey = ""
}

// FetchRcInfo GETs /json/rc_info.
func (s *Session) FetchRcInfo() (*RcInfo, error) {
	req, err := http.NewRequest(http.MethodGet, s.base()+"/json/rc_info", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "sessionKey="+s.SessionKey)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rc_info: HTTP %d: %s", res.StatusCode, truncate(raw))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	encHex, _ := m["enc_key"].(string)
	encRaw, err := hex.DecodeString(encHex)
	if err != nil {
		return nil, fmt.Errorf("rc_info enc_key: %w", err)
	}
	features, _ := m["optional_features"].(string)
	info := &RcInfo{
		EncKeyHex:        encHex,
		EncKey:           encRaw,
		RCPort:           anyInt(m["rc_port"]),
		VMPort:           anyInt(m["vm_port"]),
		OptionalFeatures: features,
		ProtocolVersion:  anyString(m["protocol_version"]),
		ServerName:       anyString(m["server_name"]),
		ILOFQDN:          anyString(m["ilo_fqdn"]),
		EncryptKey:       strings.Contains(features, "ENCRYPT_KEY"),
		EncryptVMKey:     strings.Contains(features, "ENCRYPT_VMKEY"),
	}
	if info.RCPort == 0 {
		return nil, fmt.Errorf("rc_info: missing rc_port")
	}
	return info, nil
}

func (s *Session) base() string {
	if s.Port == 443 {
		return "https://" + s.Host
	}
	return fmt.Sprintf("https://%s:%d", s.Host, s.Port)
}

func anyInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(t)
		return i
	default:
		return 0
	}
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return false
	}
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 180 {
		return s[:180] + "…"
	}
	return s
}
