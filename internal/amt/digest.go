package amt

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// digestTransport wraps RoundTripper with HTTP Digest (qop=auth) for AMT WS-MAN.
type digestTransport struct {
	user     string
	password string
	base     http.RoundTripper
	nc       uint32
}

func (t *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		req.ContentLength = int64(len(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(bodyBytes))), nil
		}
	}

	probe := req.Clone(req.Context())
	if bodyBytes != nil {
		probe.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		probe.ContentLength = int64(len(bodyBytes))
	}

	resp, err := base.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	chal := resp.Header.Get("WWW-Authenticate")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if !strings.HasPrefix(strings.ToLower(chal), "digest ") {
		return nil, fmt.Errorf("amt: expected Digest challenge, got %q", chal)
	}

	auth, err := t.authorize(req.Method, req.URL.RequestURI(), chal)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	if bodyBytes != nil {
		req2.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		req2.ContentLength = int64(len(bodyBytes))
	}
	req2.Header.Set("Authorization", auth)
	return base.RoundTrip(req2)
}

func (t *digestTransport) authorize(method, uri, challenge string) (string, error) {
	params := parseDigestChallenge(challenge)
	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	opaque := params["opaque"]
	algorithm := params["algorithm"]
	if realm == "" || nonce == "" {
		return "", fmt.Errorf("amt: incomplete Digest challenge")
	}
	if qop != "" && qop != "auth" {
		return "", fmt.Errorf("amt: unsupported Digest qop %q", qop)
	}
	useQOP := qop != ""
	nc := atomic.AddUint32(&t.nc, 1)
	ncStr := fmt.Sprintf("%08x", nc)
	cnonce, err := randomHex(8)
	if err != nil {
		return "", err
	}

	ha1 := md5Hex(t.user + ":" + realm + ":" + t.password)
	ha2 := md5Hex(method + ":" + uri)
	var response string
	if useQOP {
		response = md5Hex(ha1 + ":" + nonce + ":" + ncStr + ":" + cnonce + ":auth:" + ha2)
	} else {
		response = md5Hex(ha1 + ":" + nonce + ":" + ha2)
	}

	var b strings.Builder
	b.WriteString(`Digest username="` + t.user + `"`)
	b.WriteString(`, realm="` + realm + `"`)
	b.WriteString(`, nonce="` + nonce + `"`)
	b.WriteString(`, uri="` + uri + `"`)
	if useQOP {
		b.WriteString(`, qop=auth`)
		b.WriteString(`, nc=` + ncStr)
		b.WriteString(`, cnonce="` + cnonce + `"`)
	}
	b.WriteString(`, response="` + response + `"`)
	if opaque != "" {
		b.WriteString(`, opaque="` + opaque + `"`)
	}
	if algorithm != "" {
		b.WriteString(`, algorithm=` + algorithm)
	}
	return b.String(), nil
}

func parseDigestChallenge(h string) map[string]string {
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, ' '); i >= 0 {
		h = h[i+1:]
	}
	out := make(map[string]string)
	for _, part := range splitDigestParams(h) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		if k == "qop" {
			for _, q := range strings.Split(v, ",") {
				q = strings.TrimSpace(q)
				if q == "auth" {
					v = "auth"
					break
				}
			}
		}
		out[k] = v
	}
	return out
}

func splitDigestParams(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, strings.TrimSpace(cur.String()))
	}
	return parts
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
