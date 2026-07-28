package rc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Options configures an iLO remote-console session.
type Options struct {
	Host      string
	HTTPSPort int // 0 → 443
	User      string
	Password  string
	Insecure  bool
	Log       *slog.Logger
}

// Client owns a live IRC session (web login + console TCP + DVC decode).
type Client struct {
	opts Options
	log  *slog.Logger

	sess *Session
	ch   *Channel
	cmd  *Channel // optional; closed with client

	fb  *Framebuffer
	dec *Decoder

	// FrameHook is called with RGBX pixels when the framebuffer updates.
	FrameHook func(w, h int, pix []byte)
	Status    string
}

// Connect logs in, opens the console channel, and prepares the decoder.
// Call Run to pump video; Close when done.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.Host == "" || opts.User == "" {
		return nil, fmt.Errorf("ilo/rc: host and user required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	port := opts.HTTPSPort
	if port == 0 {
		port = 443
	}

	sess, err := Login(opts.Host, port, opts.User, opts.Password, opts.Insecure)
	if err != nil {
		return nil, err
	}
	info, err := sess.FetchRcInfo()
	if err != nil {
		sess.Close()
		return nil, err
	}

	ch, err := DialConsole(opts.Host, info.RCPort, sess.SessionKey, info, 15*time.Second)
	if err != nil {
		sess.Close()
		return nil, err
	}

	// Skip the optional command channel by default: iLO's remote-console
	// session pool is tiny, and a second TCP session often returns
	// "no free sessions" for the next client (or burns the only spare slot).

	c := &Client{
		opts: opts,
		log:  opts.Log,
		sess: sess,
		ch:   ch,
		fb:   &Framebuffer{},
	}
	c.dec = NewDecoder(c.fb, c)
	c.Status = fmt.Sprintf("%s rc_port=%d", info.ILOFQDN, info.RCPort)
	return c, nil
}

// Run pumps console bytes into the DVC decoder until ctx cancel or EOF.
// Decryption is applied one byte at a time so a mid-stream SetCipher (DVC
// header/encryption command) covers the rest of the same TCP segment — bulk
// decrypt-then-feed leaves post-cipher bytes as garbage and yields a black
// framebuffer / peer reset.
func (c *Client) Run(ctx context.Context) error {
	if c.cmd != nil {
		go c.drainCommand(ctx)
	}
	// Ask firmware for a full redraw; without this some boots stay on a blank
	// framebuffer until a natural timeout/refresh.
	_ = c.ch.Send(RefreshScreen())
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_ = c.ch.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.ch.ReadRaw(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				code := c.dec.Feed(c.ch.Decrypt(buf[i]))
				if code == 1 { // EXIT
					return io.EOF
				}
			}
		}
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}
	}
}

func (c *Client) drainCommand(ctx context.Context) {
	buf := make([]byte, 256)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = c.cmd.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err := c.cmd.Read(buf)
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			return
		}
	}
}

// Close tears down TCP and HTTPS sessions.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.ch != nil {
		_ = c.ch.Close()
	}
	if c.cmd != nil {
		_ = c.cmd.Close()
	}
	if c.sess != nil {
		c.sess.Close()
	}
	return nil
}

// Send writes a cleartext input frame on the console channel.
func (c *Client) Send(frame []byte) error {
	if c == nil || c.ch == nil {
		return fmt.Errorf("ilo/rc: not connected")
	}
	return c.ch.Send(frame)
}

// Controller callbacks -------------------------------------------------------

func (c *Client) SetCipher(cipher int) {
	if err := c.ch.SetCipher(cipher); err != nil {
		c.log.Info("ilo/rc set cipher", "cipher", cipher, "err", err)
	}
}

func (c *Client) SendAck() {
	_ = c.ch.Send(Ack())
}

func (c *Client) RefreshScreen() {
	_ = c.ch.Send(RefreshScreen())
}

func (c *Client) OnPower(on bool) {
	c.log.Debug("ilo/rc power", "on", on)
}

func (c *Client) OnHealth(level int) {}

func (c *Client) OnLicensed(flags int) {
	c.log.Debug("ilo/rc licensed", "flags", flags)
}

func (c *Client) OnFlags(flags int) {
	c.log.Debug("ilo/rc flags", "flags", flags)
}

func (c *Client) OnFramerate(fps int) {}

func (c *Client) OnStatus(field int, text string) {
	if text != "" {
		c.Status = text
	}
	c.log.Debug("ilo/rc status", "field", field, "text", text)
}

func (c *Client) OnText(text string) {}

func (c *Client) OnResize(width, height int) {
	c.log.Debug("ilo/rc resize", "w", width, "h", height)
	c.publishFrame()
}

func (c *Client) OnFrame() {
	c.publishFrame()
}

func (c *Client) OnSeize() {
	c.log.Info("ilo/rc seized by another client")
}

func (c *Client) OnExit() {
	c.log.Info("ilo/rc dvc exit")
}

func (c *Client) publishFrame() {
	if c.FrameHook == nil || c.fb == nil {
		return
	}
	w, h := c.fb.Width, c.fb.Height
	if w <= 0 || h <= 0 {
		// Keep the RFB placeholder; do not push a fake black 1024×768 frame.
		return
	}
	c.FrameHook(w, h, c.fb.ToRGBX())
}

// Ensure Client implements Controller.
var _ Controller = (*Client)(nil)
