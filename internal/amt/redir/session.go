// Package redir implements Intel AMT Hardware-KVM over the redirection
// listener (TCP 16994 / TLS 16995): StartRedirectionSession + Digest auth,
// then an RFB client that feeds Outband's RFB server / noVNC bridge.
package redir

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const authURI = "/RedirectionService"

// Options for dialing the AMT redirection listener.
type Options struct {
	Host     string
	Port     int // 0 → 16994 (or 16995 when TLS)
	TLS      bool
	User     string
	Password string
}

// Conn is an authenticated redirection socket ready for RFB.
type Conn struct {
	net.Conn
}

// Dial connects, completes StartRedirectionSession (KVMR) and Digest auth.
func Dial(opts Options) (*Conn, error) {
	port := opts.Port
	if port == 0 {
		if opts.TLS {
			port = 16995
		} else {
			port = 16994
		}
	}
	addr := net.JoinHostPort(opts.Host, fmt.Sprintf("%d", port))
	d := net.Dialer{Timeout: 10 * time.Second}
	var raw net.Conn
	var err error
	if opts.TLS {
		raw, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec
	} else {
		raw, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("amt redir dial %s: %w", addr, err)
	}
	_ = raw.SetDeadline(time.Now().Add(30 * time.Second))

	c := &session{conn: raw, user: opts.User, pass: opts.Password}
	if err := c.handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	var leftover []byte
	if len(c.buf) > 0 {
		leftover = append([]byte(nil), c.buf...)
	}
	return &Conn{Conn: &bufConn{Conn: raw, buf: leftover}}, nil
}

type session struct {
	conn   net.Conn
	user   string
	pass   string
	buf    []byte
	authed bool
}

func (s *session) handshake() error {
	// StartRedirectionSession KVM: 0x10 0x01 0x00 0x00 'K''V''M''R'
	if _, err := s.conn.Write([]byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}); err != nil {
		return fmt.Errorf("write start: %w", err)
	}
	for {
		cmd, payload, err := s.readCmd()
		if err != nil {
			return err
		}
		switch cmd {
		case 0x11: // StartRedirectionSessionReply
			if len(payload) < 1 {
				return errors.New("empty StartRedirectionSessionReply")
			}
			status := payload[0]
			if status != 0 {
				return fmt.Errorf("StartRedirectionSession status=%d", status)
			}
			// Query authentication support.
			if _, err := s.conn.Write([]byte{0x13, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
				return fmt.Errorf("write auth query: %w", err)
			}
		case 0x14: // AuthenticateSessionReply
			if err := s.handleAuthReply(payload); err != nil {
				return err
			}
			if s.authed {
				// KVM session ready: send empty 0x40 control then wait for 0x41.
				if _, err := s.conn.Write([]byte{0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
					return fmt.Errorf("write kvm ready: %w", err)
				}
			}
		case 0x41: // KVM session accepted — RFB follows
			return nil
		default:
			return fmt.Errorf("unexpected redir cmd 0x%02x during handshake", cmd)
		}
	}
}

func (s *session) handleAuthReply(payload []byte) error {
	// layout: status(1) + pad(2) + authType(1) + authDataLen(4 LE) + authData
	if len(payload) < 8 {
		return fmt.Errorf("auth reply too short (%d)", len(payload))
	}
	status := payload[0]
	authType := payload[3]
	authDataLen := int(binary.LittleEndian.Uint32(payload[4:8]))
	if len(payload) < 8+authDataLen {
		return fmt.Errorf("auth reply truncated")
	}
	authData := payload[8 : 8+authDataLen]

	switch {
	case authType == 0:
		// Query: authData is list of supported auth type bytes.
		hasDigest := false
		for _, b := range authData {
			if b == 4 {
				hasDigest = true
				break
			}
		}
		if !hasDigest {
			return errors.New("AMT redirection does not offer Digest auth")
		}
		// Empty digest request (user + uri) to obtain challenge.
		user := s.user
		uri := authURI
		totallen := len(user) + len(uri) + 8
		buf := make([]byte, 0, 9+totallen)
		buf = append(buf, 0x13, 0x00, 0x00, 0x00, 0x04)
		buf = appendU32LE(buf, uint32(totallen))
		buf = append(buf, byte(len(user)))
		buf = append(buf, user...)
		buf = append(buf, 0x00, 0x00) // empty realm
		buf = append(buf, byte(len(uri)))
		buf = append(buf, uri...)
		buf = append(buf, 0x00, 0x00, 0x00, 0x00) // empty cnonce/snc/digest placeholders
		if _, err := s.conn.Write(buf); err != nil {
			return err
		}
		return nil

	case (authType == 3 || authType == 4) && status == 1:
		realm, nonce, qop, err := parseDigestChallenge(authData, authType == 4)
		if err != nil {
			return err
		}
		cnonce := randomHex(16) // 32 hex chars — MeshCommander amt-redir randomHex(32)
		snc := "00000002"
		extra := ""
		if authType == 4 {
			extra = snc + ":" + cnonce + ":" + qop + ":"
		}
		digest := md5hex(md5hex(s.user+":"+realm+":"+s.pass) + ":" + nonce + ":" + extra + md5hex("POST:"+authURI))
		totallen := len(s.user) + len(realm) + len(nonce) + len(authURI) + len(cnonce) + len(snc) + len(digest) + 7
		if authType == 4 {
			totallen += len(qop) + 1
		}
		buf := make([]byte, 0, 9+totallen)
		buf = append(buf, 0x13, 0x00, 0x00, 0x00, authType)
		buf = appendU32LE(buf, uint32(totallen))
		buf = appendLP(buf, s.user)
		buf = appendLP(buf, realm)
		buf = appendLP(buf, nonce)
		buf = appendLP(buf, authURI)
		buf = appendLP(buf, cnonce)
		buf = appendLP(buf, snc)
		buf = appendLP(buf, digest)
		if authType == 4 {
			buf = appendLP(buf, qop)
		}
		if _, err := s.conn.Write(buf); err != nil {
			return err
		}
		return nil

	case status == 0:
		s.authed = true
		return nil

	default:
		return fmt.Errorf("AuthenticateSessionReply status=%d authType=%d", status, authType)
	}
}

func (s *session) readCmd() (byte, []byte, error) {
	for {
		if err := s.fill(1); err != nil {
			return 0, nil, err
		}
		cmd := s.buf[0]
		switch cmd {
		case 0x11:
			if err := s.fill(13); err != nil {
				return 0, nil, err
			}
			oemlen := int(s.buf[12])
			need := 13 + oemlen
			if err := s.fill(need); err != nil {
				return 0, nil, err
			}
			// payload for handler: status at [1], rest...
			payload := append([]byte(nil), s.buf[1:need]...)
			s.consume(need)
			return cmd, payload, nil

		case 0x14:
			if err := s.fill(9); err != nil {
				return 0, nil, err
			}
			authDataLen := int(binary.LittleEndian.Uint32(s.buf[5:9]))
			need := 9 + authDataLen
			if err := s.fill(need); err != nil {
				return 0, nil, err
			}
			// status, pad, authType, len already in [1:9]; pass [1:]
			payload := append([]byte(nil), s.buf[1:need]...)
			s.consume(need)
			return cmd, payload, nil

		case 0x41:
			if err := s.fill(8); err != nil {
				return 0, nil, err
			}
			s.consume(8)
			return cmd, nil, nil

		default:
			return 0, nil, fmt.Errorf("unknown redir opcode 0x%02x", cmd)
		}
	}
}

func (s *session) fill(n int) error {
	for len(s.buf) < n {
		tmp := make([]byte, 4096)
		nr, err := s.conn.Read(tmp)
		if nr > 0 {
			s.buf = append(s.buf, tmp[:nr]...)
		}
		if err != nil {
			if err == io.EOF && len(s.buf) >= n {
				return nil
			}
			return err
		}
	}
	return nil
}

func (s *session) consume(n int) {
	s.buf = s.buf[n:]
}

func parseDigestChallenge(authData []byte, withQOP bool) (realm, nonce, qop string, err error) {
	cur := 0
	readLP := func() (string, error) {
		if cur >= len(authData) {
			return "", errors.New("digest challenge truncated")
		}
		l := int(authData[cur])
		cur++
		if cur+l > len(authData) {
			return "", errors.New("digest challenge field overflow")
		}
		s := string(authData[cur : cur+l])
		cur += l
		return s, nil
	}
	realm, err = readLP()
	if err != nil {
		return
	}
	nonce, err = readLP()
	if err != nil {
		return
	}
	if withQOP {
		qop, err = readLP()
	}
	return
}

func appendU32LE(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendLP(b []byte, s string) []byte {
	b = append(b, byte(len(s)))
	return append(b, s...)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", nBytes*2, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// bufConn prepends leftover handshake bytes before reading from the socket.
type bufConn struct {
	net.Conn
	buf []byte
}

func (c *bufConn) Read(p []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
