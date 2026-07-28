package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "mipmi_session"
	sessionTTL    = 12 * time.Hour
)

// Identity is optional operator info stored with a session (OIDC logins).
type Identity struct {
	Subject string
	Email   string
	Name    string
}

type session struct {
	Expires  time.Time
	Identity Identity
}

// Gate is a UI auth gate. BMC credentials never reach the browser.
// Password and/or OIDC may be enabled; both issue the same session cookie.
type Gate struct {
	pass   string
	secret []byte

	mu       sync.Mutex
	sessions map[string]session
}

// NewGate creates an auth gate. pass may be empty when OIDC-only; when set it
// is compared with constant time for break-glass / sole local login.
func NewGate(pass string) (*Gate, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &Gate{
		pass:     pass,
		secret:   secret,
		sessions: make(map[string]session),
	}, nil
}

func (g *Gate) passwordEnabled() bool {
	return g.pass != ""
}

func (g *Gate) validPassword(got string) bool {
	if g.pass == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(g.pass)) == 1
}

func (g *Gate) issueToken() (string, time.Time) {
	return g.issueTokenFor(Identity{})
}

func (g *Gate) issueTokenFor(id Identity) (string, time.Time) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	mac := hmac.New(sha256.New, g.secret)
	mac.Write(raw)
	token := hex.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
	exp := time.Now().Add(sessionTTL)
	g.mu.Lock()
	g.sessions[token] = session{Expires: exp, Identity: id}
	g.mu.Unlock()
	return token, exp
}

func (g *Gate) checkToken(token string) bool {
	if token == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(s.Expires) {
		delete(g.sessions, token)
		return false
	}
	return true
}

func (g *Gate) revoke(token string) {
	g.mu.Lock()
	delete(g.sessions, token)
	g.mu.Unlock()
}

func authExempt(path string) bool {
	switch path {
	case "/login", "/logout":
		return true
	}
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	if strings.HasPrefix(path, "/auth/oidc/") {
		return true
	}
	return false
}

// Middleware redirects unauthenticated HTML requests to /login.
func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if authExempt(path) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || !g.checkToken(c.Value) {
			if isHTMX(r) || wantsJSON(r) || strings.HasPrefix(path, "/ws/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) setSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
	})
}

func (g *Gate) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func wantsJSON(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}
