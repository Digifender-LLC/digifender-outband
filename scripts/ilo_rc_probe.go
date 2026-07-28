//go:build ignore

package main

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	host := env("OUTBAND_BMC_HOST", "192.168.9.90")
	user := env("OUTBAND_BMC_USER", "Administrator")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "OUTBAND_BMC_PASS required")
		os.Exit(2)
	}
	port := 443
	if v := os.Getenv("OUTBAND_BMC_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "OUTBAND_BMC_PORT: %v\n", err)
			os.Exit(2)
		}
		port = p
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // probe against self-signed iLO
		},
	}
	base := fmt.Sprintf("https://%s:%d", host, port)

	sessionKey, err := login(client, base, user, pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("login ok session_key_len=%d\n", len(sessionKey))

	rc, err := getJSON(client, base+"/json/rc_info", sessionKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rc_info: %v\n", err)
		os.Exit(1)
	}
	encKey, _ := rc["enc_key"].(string)
	features, _ := rc["optional_features"].(string)
	rcPort := intFrom(rc["rc_port"])
	fmt.Printf("rc_info rc_port=%d features=%q protocol=%v\n", rcPort, features, rc["protocol_version"])

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(rcPort)), 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial rc: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	hello := make([]byte, 1)
	if _, err := io.ReadFull(conn, hello); err != nil {
		fmt.Fprintf(os.Stderr, "hello: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("hello 0x%02x\n", hello[0])
	if hello[0] != 0x50 {
		fmt.Fprintln(os.Stderr, "expected hello 0x50")
		os.Exit(1)
	}

	req := buildConnectRequest(0x2001, sessionKey, encKey, features)
	if _, err := conn.Write(req); err != nil {
		fmt.Fprintf(os.Stderr, "send auth: %v\n", err)
		os.Exit(1)
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(conn, resp); err != nil {
		fmt.Fprintf(os.Stderr, "auth resp: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("auth 0x%02x (%c)\n", resp[0], resp[0])
	if resp[0] == 0x53 || resp[0] == 0x59 {
		if _, err := conn.Write([]byte{0x55, 0x00}); err != nil {
			fmt.Fprintf(os.Stderr, "seize: %v\n", err)
			os.Exit(1)
		}
		if _, err := io.ReadFull(conn, resp); err != nil {
			fmt.Fprintf(os.Stderr, "seize resp: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("seize 0x%02x\n", resp[0])
	}
	if resp[0] != 0x52 {
		fmt.Fprintln(os.Stderr, "handshake failed")
		os.Exit(1)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 256)
	n, _ := io.ReadFull(conn, buf[:16])
	if n == 0 {
		n, _ = conn.Read(buf)
	}
	fmt.Printf("first_bytes n=%d hex=%s\n", n, hex.EncodeToString(buf[:n]))

	_ = logout(client, base, sessionKey)
	fmt.Println("ok")
}

func buildConnectRequest(cmd int, sessionKey, encKey, features string) []byte {
	lo := byte(cmd & 0xff)
	hi := byte((cmd >> 8) & 0xff)
	token := []byte(sessionKey)
	if strings.Contains(features, "ENCRYPT_KEY") {
		enc := []byte(encKey)
		for i := range token {
			token[i] ^= enc[i%len(enc)]
		}
		if strings.Contains(features, "ENCRYPT_VMKEY") {
			hi |= 0x40
		} else {
			hi |= 0x80
		}
	}
	out := make([]byte, 2+len(token))
	out[0], out[1] = lo, hi
	copy(out[2:], token)
	return out
}

func login(c *http.Client, base, user, pass string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"method": "login", "user_login": user, "password": pass,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/json/login_session", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", res.StatusCode, truncate(raw))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	sk, _ := m["session_key"].(string)
	if sk == "" {
		return "", fmt.Errorf("no session_key in %s", truncate(raw))
	}
	return sk, nil
}

func logout(c *http.Client, base, sessionKey string) error {
	body, _ := json.Marshal(map[string]string{"method": "logout", "session_key": sessionKey})
	req, err := http.NewRequest(http.MethodPost, base+"/json/login_session", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "sessionKey="+sessionKey)
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func getJSON(c *http.Client, url, sessionKey string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "sessionKey="+sessionKey)
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, truncate(raw))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func intFrom(v any) int {
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

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
