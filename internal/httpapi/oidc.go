package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"outband/internal/config"
)

const (
	oidcStateCookie = "outband_oidc_state"
	oidcNonceCookie = "outband_oidc_nonce"
	oidcPKCECookie  = "outband_oidc_pkce"
	oidcCookieTTL   = 10 * time.Minute
)

// oidcAuth handles OpenID Connect Authorization Code + PKCE for the UI gate.
type oidcAuth struct {
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
}

func newOIDCAuth(ctx context.Context, cfg config.OIDCConfig) (*oidcAuth, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	issuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", issuer, err)
	}
	oauthCfg := oauth2.Config{
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  strings.TrimSpace(cfg.RedirectURL),
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	return &oidcAuth{
		verifier: provider.Verifier(&oidc.Config{ClientID: oauthCfg.ClientID}),
		oauth2:   oauthCfg,
	}, nil
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Error(w, "OIDC not configured", http.StatusNotFound)
		return
	}
	state, err := randomURLSafe(32)
	if err != nil {
		s.log.Error("oidc state", "err", err)
		http.Error(w, "oidc error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomURLSafe(32)
	if err != nil {
		s.log.Error("oidc nonce", "err", err)
		http.Error(w, "oidc error", http.StatusInternalServerError)
		return
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		s.log.Error("oidc pkce", "err", err)
		http.Error(w, "oidc error", http.StatusInternalServerError)
		return
	}
	setOIDCCookie(w, oidcStateCookie, state)
	setOIDCCookie(w, oidcNonceCookie, nonce)
	setOIDCCookie(w, oidcPKCECookie, verifier)

	challenge := pkceChallenge(verifier)
	url := s.oidc.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Error(w, "OIDC not configured", http.StatusNotFound)
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		desc := r.URL.Query().Get("error_description")
		s.log.Warn("oidc callback error", "error", errMsg, "description", desc)
		clearOIDCCookies(w)
		w.WriteHeader(http.StatusUnauthorized)
		d := s.page("Login", "")
		d.Error = "SSO login failed"
		s.render(w, "login.html", d)
		return
	}

	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" {
		s.oidcLoginError(w, "missing OIDC state")
		return
	}
	if !constantTimeEqual(stateCookie.Value, r.URL.Query().Get("state")) {
		s.oidcLoginError(w, "OIDC state mismatch")
		return
	}
	nonceCookie, err := r.Cookie(oidcNonceCookie)
	if err != nil || nonceCookie.Value == "" {
		s.oidcLoginError(w, "missing OIDC nonce")
		return
	}
	pkceCookie, err := r.Cookie(oidcPKCECookie)
	if err != nil || pkceCookie.Value == "" {
		s.oidcLoginError(w, "missing OIDC PKCE verifier")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.oidcLoginError(w, "missing authorization code")
		return
	}

	clearOIDCCookies(w)

	ctx := r.Context()
	token, err := s.oidc.oauth2.Exchange(ctx, code, oauth2.VerifierOption(pkceCookie.Value))
	if err != nil {
		s.log.Error("oidc token exchange", "err", err)
		s.oidcLoginError(w, "token exchange failed")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		s.oidcLoginError(w, "missing id_token")
		return
	}
	idToken, err := s.oidc.verifier.Verify(ctx, rawID)
	if err != nil {
		s.log.Error("oidc id_token verify", "err", err)
		s.oidcLoginError(w, "id_token verification failed")
		return
	}
	if idToken.Nonce != nonceCookie.Value {
		s.oidcLoginError(w, "OIDC nonce mismatch")
		return
	}

	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	_ = idToken.Claims(&claims)
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	id := Identity{
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    name,
	}
	sess, exp := s.gate.issueTokenFor(id)
	s.gate.setSessionCookie(w, sess, exp)
	s.log.Info("oidc login", "sub", id.Subject, "email", id.Email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) oidcLoginError(w http.ResponseWriter, msg string) {
	s.log.Warn("oidc callback", "err", msg)
	clearOIDCCookies(w)
	w.WriteHeader(http.StatusUnauthorized)
	d := s.page("Login", "")
	d.Error = "SSO login failed"
	s.render(w, "login.html", d)
}

func setOIDCCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oidcCookieTTL.Seconds()),
		Expires:  time.Now().Add(oidcCookieTTL),
	})
}

func clearOIDCCookies(w http.ResponseWriter) {
	for _, name := range []string{oidcStateCookie, oidcNonceCookie, oidcPKCECookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/auth/oidc",
			HttpOnly: true,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
