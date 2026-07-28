package rc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	helloByte = 0x50

	respDenied     = 0x51
	respAuth       = 0x52
	respBusyA      = 0x53
	respBusyB      = 0x59
	respNoLicense  = 0x57
	respNoSessions = 0x58

	connectSeize = 0x0055
	connectShare = 0x0056

	cmdConsole = 0x2001
	cmdCommand = 0x2002
)

// Channel is one authenticated TCP console (or command) connection.
type Channel struct {
	conn net.Conn

	encKey        []byte
	dvcEncrypt    bool
	decryptStream Keystream
	encryptStream Keystream
}

// DialConsole opens TCP to host:rcPort and completes the console handshake (seize on busy).
func DialConsole(host string, rcPort int, sessionKey string, info *RcInfo, timeout time.Duration) (*Channel, error) {
	return dialChannel(host, rcPort, sessionKey, info, cmdConsole, timeout)
}

// DialCommand opens the out-of-band command channel (optional).
func DialCommand(host string, rcPort int, sessionKey string, info *RcInfo, timeout time.Duration) (*Channel, error) {
	return dialChannel(host, rcPort, sessionKey, info, cmdCommand, timeout)
}

func dialChannel(host string, rcPort int, sessionKey string, info *RcInfo, cmd int, timeout time.Duration) (*Channel, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", rcPort))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	hello := make([]byte, 1)
	if _, err := io.ReadFull(conn, hello); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rc hello: %w", err)
	}
	if hello[0] != helloByte {
		conn.Close()
		return nil, fmt.Errorf("rc hello: got 0x%02x want 0x50", hello[0])
	}

	req := buildConnectRequest(cmd, sessionKey, info)
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rc auth: %w", err)
	}
	switch resp[0] {
	case respAuth:
		// ok
	case respBusyA, respBusyB:
		seize := []byte{byte(connectSeize & 0xff), byte(connectSeize >> 8)}
		if _, err := conn.Write(seize); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := io.ReadFull(conn, resp); err != nil {
			conn.Close()
			return nil, err
		}
		if resp[0] != respAuth {
			conn.Close()
			return nil, fmt.Errorf("rc seize refused (0x%02x)", resp[0])
		}
	case respDenied:
		conn.Close()
		return nil, fmt.Errorf("rc access denied")
	case respNoLicense:
		conn.Close()
		return nil, fmt.Errorf("rc no remote-console license")
	case respNoSessions:
		conn.Close()
		return nil, fmt.Errorf("rc no free sessions")
	default:
		conn.Close()
		return nil, fmt.Errorf("rc unexpected auth 0x%02x", resp[0])
	}

	_ = conn.SetDeadline(time.Time{})
	return &Channel{conn: conn, encKey: append([]byte(nil), info.EncKey...)}, nil
}

func buildConnectRequest(cmd int, sessionKey string, info *RcInfo) []byte {
	lo := byte(cmd & 0xff)
	hi := byte((cmd >> 8) & 0xff)
	token := XORSessionKey(sessionKey, info.EncKeyHex, info.EncryptKey)
	if info.EncryptKey {
		if info.EncryptVMKey {
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

// SetCipher selects the stream cipher for both directions.
func (c *Channel) SetCipher(cipher int) error {
	dec, err := MakeKeystream(cipher, c.encKey)
	if err != nil {
		return err
	}
	enc, err := MakeKeystream(cipher, c.encKey)
	if err != nil {
		return err
	}
	c.decryptStream = dec
	c.encryptStream = enc
	c.dvcEncrypt = cipher != CipherNone
	return nil
}

// ChangeKey re-runs RC4 KSA on both streams (DVC key-change).
func (c *Channel) ChangeKey() {
	if c.decryptStream != nil {
		c.decryptStream.UpdateKey()
	}
	if c.encryptStream != nil {
		c.encryptStream.UpdateKey()
	}
}

// Send writes an input frame, XOR-encrypting when active.
func (c *Channel) Send(frame []byte) error {
	if c == nil || c.conn == nil {
		return net.ErrClosed
	}
	out := frame
	if c.dvcEncrypt && c.encryptStream != nil {
		out = make([]byte, len(frame))
		for i, b := range frame {
			out[i] = b ^ c.encryptStream.RandomValue()
		}
	}
	_, err := c.conn.Write(out)
	return err
}

// ReadRaw reads ciphertext (or cleartext) bytes without applying the stream cipher.
// Callers that feed the DVC decoder must decrypt one byte at a time via Decrypt
// so SetCipher mid-stream affects subsequent bytes in the same TCP segment.
func (c *Channel) ReadRaw(p []byte) (int, error) {
	if c == nil || c.conn == nil {
		return 0, net.ErrClosed
	}
	return c.conn.Read(p)
}

// Decrypt applies the active server→client keystream to one wire byte.
// Must be called once per received byte, in order, before DVC Feed.
func (c *Channel) Decrypt(b byte) byte {
	if c.dvcEncrypt && c.decryptStream != nil {
		return b ^ c.decryptStream.RandomValue()
	}
	return b
}

// Read decrypts into p one byte at a time (safe across SetCipher boundaries).
func (c *Channel) Read(p []byte) (int, error) {
	n, err := c.ReadRaw(p)
	for i := 0; i < n; i++ {
		p[i] = c.Decrypt(p[i])
	}
	return n, err
}

// Close closes the TCP socket.
func (c *Channel) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// SetReadDeadline sets the underlying conn deadline.
func (c *Channel) SetReadDeadline(t time.Time) error {
	if c == nil || c.conn == nil {
		return net.ErrClosed
	}
	return c.conn.SetReadDeadline(t)
}

// Discard drains up to n bytes (command channel keepalive helper).
func Discard(r io.Reader, n int) {
	buf := make([]byte, 256)
	for n > 0 {
		chunk := len(buf)
		if chunk > n {
			chunk = n
		}
		nr, err := r.Read(buf[:chunk])
		n -= nr
		if err != nil {
			return
		}
	}
}

// ReadCmdHeader reads a 12-byte command-channel header (optional path).
func ReadCmdHeader(r io.Reader) (typ byte, size int, err error) {
	var hdr [12]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, err
	}
	typ = hdr[0]
	size = int(binary.LittleEndian.Uint32(hdr[4:8]))
	return typ, size, nil
}
