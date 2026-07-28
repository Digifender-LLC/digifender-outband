package kvm

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"mipmi/internal/kvm/codec"
)

const keepAliveInterval = 5 * time.Second

// maxFrameBytes caps the reassembled video frame. A 1080p ASPEED frame is well
// under this; the bound just stops a malformed or hostile stream (huge h.Size,
// or an endless run of non-terminal fragments) from growing the buffer until it
// exhausts memory before the codec can reject it.
const maxFrameBytes = 8 << 20 // 8 MiB

// FrameFunc is called with each fully decoded video frame.
type FrameFunc func(f *codec.Frame)

// Client speaks the BMC's IVTP KVM protocol over one TCP/TLS socket.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex // serializes writes
	dec  *codec.Decoder

	webHost   string // for releasing the web session on close
	webCookie string
	webToken  string            // STOKEN, reused for virtual-media auth
	webArgs   map[string]string // parsed jnlp args (vmedia ports, vmsecure)

	OnFrame FrameFunc // optional; invoked on each decoded frame
}

// Options configure a KVM connection.
type Options struct {
	Host string
	Port int  // video port, default 7578 (Tyan/older AMI); newer MegaRAC often 7582
	TLS  bool // kvmsecure (usually false on older cleartext Adviser ports)
	User string
}

// Connect logs into the BMC web UI, opens the video socket, and completes the
// IVTP handshake (validate → resume). The password is used only for the web
// login and is never stored or logged.
func Connect(ctx context.Context, opts Options, password string) (*Client, error) {
	if opts.Port == 0 {
		opts.Port = 7578
	}

	// FetchLaunchArgs (rather than Login) so we keep the full jnlp arg map: the
	// virtual-media path reuses this session's token and needs the per-device ports
	// and vmsecure flag carried in the same response.
	args, cookie, err := FetchLaunchArgs(ctx, opts.Host, opts.User, password)
	if err != nil {
		return nil, err
	}
	if p := args["kvmport"]; p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n > 0 {
			opts.Port = n
		}
	}
	token := args["kvmtoken"]
	if token == "" {
		Logout(opts.Host, cookie)
		return nil, fmt.Errorf("launch jnlp: no -kvmtoken in response (parsed %d args)", len(args))
	}
	webcookie := args["webcookie"]
	if webcookie == "" {
		webcookie = cookie
	}
	sess := WebSession{Token: token, Cookie: webcookie}
	log.Printf("kvm: web session established (token %d bytes)", len(token))

	conn, err := dial(opts.Host, opts.Port, opts.TLS)
	if err != nil {
		Logout(opts.Host, webcookie)
		return nil, fmt.Errorf("dial video port: %w", err)
	}

	c := &Client{
		conn:      conn,
		r:         bufio.NewReaderSize(conn, 1<<16),
		dec:       codec.New(),
		webHost:   opts.Host,
		webCookie: webcookie,
		webToken:  token,
		webArgs:   args,
	}

	// Bound the handshake: after the TCP/TLS dial, a BMC that stops responding
	// mid-handshake would otherwise block this goroutine forever (the read loop
	// itself is unbounded by design — frames arrive continuously). Clear the
	// deadline once validated so the steady video stream is not time-limited.
	conn.SetDeadline(time.Now().Add(dialTimeout))
	if err := c.handshake(sess, opts.User); err != nil {
		conn.Close()
		Logout(opts.Host, webcookie)
		return nil, err
	}
	conn.SetDeadline(time.Time{})

	// Keep the web session alive. It minted the kvmtoken and allocated the video
	// session, but virtual media reuses that same token to authenticate its IUSB
	// redirection (internal/kvm/vmediactl.go), so we no longer release it after the
	// handshake. The session is logged out when the KVM client closes (Close),
	// tearing down video and virtual media together.
	log.Printf("kvm: web session retained for virtual-media reuse")
	return c, nil
}

// VMediaSession returns the live web-session token and the parsed jnlp arg map
// (per-device vmedia ports, vmsecure). The virtual-media path uses these to open an
// IUSB redirection by reusing this KVM session instead of opening a new web login.
func (c *Client) VMediaSession() (token string, args map[string]string) {
	return c.webToken, c.webArgs
}

func (c *Client) handshake(sess WebSession, user string) error {
	_ = user // legacy validate body is MD5(token) only
	// Tyan Adviser: client sends VALIDATE immediately after TCP connect.
	pkt := buildValidatePacket(sess.Token)
	if err := c.write(pkt); err != nil {
		return fmt.Errorf("send validate: %w", err)
	}
	log.Printf("kvm: sent validate (MD5 token, %d header + %d hash)", HeaderSize, sessionTokenLen)

	_ = c.conn.SetReadDeadline(time.Now().Add(dialTimeout))
	defer c.conn.SetReadDeadline(time.Time{})

	for {
		h, err := readHeader(c.r)
		if err != nil {
			return fmt.Errorf("handshake read: %w", err)
		}
		switch h.Type {
		case opValidateVideoResp:
			body := make([]byte, h.Size)
			if h.Size > 0 {
				if _, err := io.ReadFull(c.r, body); err != nil {
					return fmt.Errorf("read validate body: %w", err)
				}
			}
			// OnValidateVideoSessionResp: byte==0 → auth failure; nonzero → OK.
			status := byte(0)
			if len(body) > 0 {
				status = body[0]
			} else if h.Status != 0 {
				status = byte(h.Status)
			}
			if status == 0 {
				return fmt.Errorf("session rejected by BMC (status %d body=%v)", h.Status, body)
			}
			log.Printf("kvm: video session validated (status=%d)", status)
			// Legacy path starts redirection without a separate RESUME packet;
			// still send RESUME for firmware that expects it (harmless if ignored).
			_ = c.sendHeader(opResumeRedirection, 0)
			return nil

		case opStopSession:
			return fmt.Errorf("BMC refused session (stop, status %d)", h.Status)

		default:
			if h.Size > 0 {
				if _, err := c.r.Discard(int(h.Size)); err != nil {
					return fmt.Errorf("skip handshake msg %d: %w", h.Type, err)
				}
			}
		}
	}
}

// Run drives the read loop and a keep-alive ticker until ctx is cancelled or the
// socket fails.
func (c *Client) Run(ctx context.Context) error {
	go c.keepAlive(ctx)
	defer c.Close()

	errc := make(chan error, 1)
	go func() { errc <- c.readLoop() }()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

func (c *Client) readLoop() error {
	var frame []byte
	seq := 0
	for {
		h, err := readHeader(c.r)
		if err != nil {
			return err
		}
		switch h.Type {
		case opVideoFragment:
			if h.Size < 2 {
				if _, err := c.r.Discard(int(h.Size)); err != nil {
					return err
				}
				continue
			}
			var fn [2]byte
			if _, err := io.ReadFull(c.r, fn[:]); err != nil {
				return err
			}
			fragNum := binary.LittleEndian.Uint16(fn[:])
			dataLen := int(h.Size) - 2

			if fragNum&0x7fff == 0 { // first fragment of a frame
				frame = frame[:0]
			}
			start := len(frame)
			if start+dataLen > maxFrameBytes {
				return fmt.Errorf("video frame exceeds %d bytes", maxFrameBytes)
			}
			frame = append(frame, make([]byte, dataLen)...)
			if _, err := io.ReadFull(c.r, frame[start:]); err != nil {
				return err
			}

			if fragNum&0x8000 != 0 { // last fragment → frame complete
				seq++
				c.handleFrame(seq, frame)
				frame = frame[:0]
			}

		default:
			if h.Size > 0 {
				if _, err := c.r.Discard(int(h.Size)); err != nil {
					return err
				}
			}
			// TODO(kvm): dispatch control messages (power, LED, encryption...).
		}
	}
}

func (c *Client) handleFrame(seq int, frame []byte) {
	f, err := c.dec.Decode(frame)
	if err != nil {
		if seq == 1 || seq%60 == 0 {
			log.Printf("kvm: frame %d, %d bytes (decode failed: %v)", seq, len(frame), err)
		}
		return
	}
	if seq == 1 || seq%120 == 0 {
		log.Printf("kvm: decoded frame %d (%dx%d)", seq, f.W, f.H)
	}
	if c.OnFrame != nil {
		c.OnFrame(f)
	}
}

func (c *Client) keepAlive(ctx context.Context) {
	t := time.NewTicker(keepAliveInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// No dedicated keep-alive opcode on this firmware; poll keyboard LEDs.
			if err := c.sendHeader(opGetKeybdLED, 0); err != nil {
				return
			}
		}
	}
}

// sendHeader sends a bodyless IVTP packet.
func (c *Client) sendHeader(typ, status uint8) error {
	return c.write(header{Type: typ, Status: uint16(status)}.marshal())
}

func (c *Client) write(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(b)
	return err
}

// Close tears down the socket and releases the BMC web session.
func (c *Client) Close() error {
	err := c.conn.Close()
	Logout(c.webHost, c.webCookie)
	c.webCookie = ""
	return err
}
