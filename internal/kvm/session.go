package kvm

import (
	"context"
	"crypto/md5"

	"mipmi/internal/amiweb"
)

// WebSession is the result of logging into the BMC web UI.
type WebSession struct {
	Token  string // kvmtoken — MD5'd for IVTP validate on Tyan
	Cookie string // web SessionCookie
}

// Login authenticates via AMI web UI and allocates a video session token.
func Login(ctx context.Context, host, user, password string) (WebSession, error) {
	s, err := amiweb.Login(ctx, host, user, password)
	if err != nil {
		return WebSession{}, err
	}
	return WebSession{Token: s.KVMToken, Cookie: s.WebCookie}, nil
}

// FetchLaunchArgs returns the parsed JNLP map and create.asp cookie.
func FetchLaunchArgs(ctx context.Context, host, user, password string) (map[string]string, string, error) {
	return amiweb.FetchLaunchArgs(ctx, host, user, password)
}

// Logout best-effort releases a BMC web session.
func Logout(host, cookie string) { amiweb.Logout(host, cookie) }

// buildValidatePacket builds the Tyan/legacy VALIDATE_VIDEO_SESSION packet:
// 7-byte header (type=34, size=16) + MD5(session_token) [16 bytes].
func buildValidatePacket(token string) []byte {
	sum := md5.Sum([]byte(token))
	h := header{Type: opValidateVideo, Size: sessionTokenLen, Status: 0}
	buf := make([]byte, HeaderSize+sessionTokenLen)
	copy(buf, h.marshal())
	copy(buf[HeaderSize:], sum[:])
	return buf
}
