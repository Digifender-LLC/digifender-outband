// Package amiweb authenticates to AMI MegaRAC GoAhead web UIs and extracts
// JViewer launch parameters for KVM (IVTP) sessions.
package amiweb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Session holds web cookie and KVM video token minted by the BMC.
type Session struct {
	Cookie    string // SessionCookie / WEBVAR SESSION_COOKIE
	KVMToken  string // JNLP kvmtoken (opaque)
	WebCookie string // JNLP webcookie (often same as Cookie)
	Host      string // BMC host from JNLP when present
	Port      int    // video port from JNLP when present (else 0)
}

var (
	reCookie = regexp.MustCompile(`'SESSION_COOKIE'\s*:\s*'([^']*)'`)
	reArg    = regexp.MustCompile(`(?s)<argument>(.*?)</argument>`)
)

// Login creates a web session and fetches jviewer.jnlp to allocate a video token.
//
// Older Tyan/AMI firmwares emit positional JNLP args (host, port, token, cookie)
// and a corrupt 0x02 byte between token and cookie; named -kvmtoken/-webcookie
// pairs (newer MegaRAC) are also accepted.
func Login(ctx context.Context, host, user, password string) (Session, error) {
	args, cookie, err := FetchLaunchArgs(ctx, host, user, password)
	if err != nil {
		return Session{}, err
	}
	tok := args["kvmtoken"]
	if tok == "" {
		return Session{}, fmt.Errorf("amiweb: no kvmtoken in jnlp (keys=%v)", keys(args))
	}
	web := args["webcookie"]
	if web == "" {
		web = cookie
	}
	port := 0
	if p := args["kvmport"]; p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	return Session{
		Cookie:    cookie,
		KVMToken:  tok,
		WebCookie: web,
		Host:      firstNonEmpty(args["hostname"], args["host"], host),
		Port:      port,
	}, nil
}

// FetchLaunchArgs performs WEBSES create + jviewer.jnlp fetch.
func FetchLaunchArgs(ctx context.Context, host, user, password string) (map[string]string, string, error) {
	hc := &http.Client{Timeout: 20 * time.Second}
	base := "http://" + host

	form := url.Values{"WEBVAR_USERNAME": {user}, "WEBVAR_PASSWORD": {password}}
	body, err := httpDo(ctx, hc, http.MethodPost, base+"/rpc/WEBSES/create.asp",
		strings.NewReader(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		return nil, "", fmt.Errorf("amiweb login: %w", err)
	}
	m := reCookie.FindStringSubmatch(body)
	if m == nil {
		return nil, "", fmt.Errorf("amiweb login: no SESSION_COOKIE (bad credentials?)")
	}
	cookie := m[1]

	jnlpURL := base + "/Java/jviewer.jnlp?EXTRNIP=" + url.QueryEscape(host) + "&JNLPSTR=JViewer"
	body, err = httpDo(ctx, hc, http.MethodGet, jnlpURL, nil,
		map[string]string{"Cookie": "SessionCookie=" + cookie})
	if err != nil {
		return nil, "", fmt.Errorf("amiweb jnlp: %w", err)
	}
	if strings.Contains(body, "session_expired") {
		return nil, "", fmt.Errorf("amiweb jnlp: session_expired — BMC web session pool may be full")
	}
	return ParseJNLPArgs(body, cookie), cookie, nil
}

// Logout best-effort releases a BMC web session.
func Logout(host, cookie string) {
	if cookie == "" {
		return
	}
	hc := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://"+host+"/rpc/WEBSES/logout.asp", nil)
	if err != nil {
		return
	}
	req.Header.Set("Cookie", "SessionCookie="+cookie)
	if resp, err := hc.Do(req); err == nil {
		resp.Body.Close()
	}
}

// ParseJNLPArgs extracts kvmtoken / webcookie / port from a JViewer jnlp body.
func ParseJNLPArgs(body, fallbackCookie string) map[string]string {
	// Repair Tyan corruption: 0x02 spliced before the next <argument> tag.
	body = strings.ReplaceAll(body, "\x02<argument>", "</argument><argument>")
	body = strings.ReplaceAll(body, "\x02", "")

	ms := reArg.FindAllStringSubmatch(body, -1)
	raw := make([]string, 0, len(ms))
	for _, m := range ms {
		v := strings.TrimSpace(m[1])
		v = strings.Map(func(r rune) rune {
			if r == 0 || (!unicode.IsPrint(r) && !unicode.IsSpace(r)) {
				return -1
			}
			return r
		}, v)
		v = strings.TrimSpace(v)
		if v != "" {
			raw = append(raw, v)
		}
	}

	out := make(map[string]string)

	// Named pairs: -kvmtoken / value (newer MegaRAC).
	for i := 0; i+1 < len(raw); i += 2 {
		name := strings.TrimPrefix(raw[i], "-")
		if strings.HasPrefix(raw[i], "-") || looksLikeFlag(raw[i]) {
			out[name] = raw[i+1]
		}
	}

	// Positional Tyan/AMI: host, port, kvmtoken, webcookie.
	if out["kvmtoken"] == "" && len(raw) >= 3 {
		out["hostname"] = raw[0]
		out["kvmport"] = raw[1]
		out["kvmtoken"] = raw[2]
		if len(raw) >= 4 {
			out["webcookie"] = raw[3]
		} else if fallbackCookie != "" {
			out["webcookie"] = fallbackCookie
		}
	}
	if out["webcookie"] == "" && fallbackCookie != "" {
		out["webcookie"] = fallbackCookie
	}
	return out
}

func looksLikeFlag(s string) bool {
	return strings.HasPrefix(s, "-") && len(s) > 1 && unicode.IsLetter(rune(s[1]))
}

func httpDo(ctx context.Context, hc *http.Client, method, u string, body io.Reader, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// AMI GoAhead often lies about Content-Length on jviewer.jnlp; accept partial bodies.
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if len(b) == 0 && err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(b), nil
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
