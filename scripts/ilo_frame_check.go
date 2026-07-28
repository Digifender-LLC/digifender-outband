//go:build ignore

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"outband/internal/ilo/rc"
)

func main() {
	host := os.Getenv("OUTBAND_BMC_HOST")
	user := os.Getenv("OUTBAND_BMC_USER")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "OUTBAND_BMC_PASS required")
		os.Exit(2)
	}

	sess, err := rc.Login(host, 443, user, pass, true)
	if err != nil {
		fmt.Println("login", err)
		os.Exit(1)
	}
	defer sess.Close()
	info, err := sess.FetchRcInfo()
	if err != nil {
		fmt.Println("rcinfo", err)
		os.Exit(1)
	}
	fmt.Println("features", info.OptionalFeatures, "rc", info.RCPort)

	ch, err := rc.DialConsole(host, info.RCPort, sess.SessionKey, info, 15*time.Second)
	if err != nil {
		fmt.Println("dial", err)
		os.Exit(1)
	}
	defer ch.Close()

	fb := &rc.Framebuffer{}
	ctl := &liveCtl{ch: ch, fb: fb}
	dec := rc.NewDecoder(fb, ctl)

	buf := make([]byte, 4096)
	var rawFirst, decFirst []byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = ch.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := ch.ReadRaw(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				raw := buf[i]
				plain := ch.Decrypt(raw)
				if len(rawFirst) < 32 {
					rawFirst = append(rawFirst, raw)
					decFirst = append(decFirst, plain)
				}
				_ = dec.Feed(plain)
			}
		}
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			fmt.Println("read", err)
			break
		}
		if fb.Width > 0 && ctl.frames >= 3 {
			break
		}
	}
	fmt.Println("raw", hex.EncodeToString(rawFirst))
	fmt.Println("dec", hex.EncodeToString(decFirst))
	fmt.Println("cipher", ctl.cipher, "frames", ctl.frames, "size", fb.Width, fb.Height, "nz", nonzero(fb))
	if ctl.cipher == 0 || fb.Width == 0 || nonzero(fb) == 0 {
		os.Exit(1)
	}
}

func nonzero(fb *rc.Framebuffer) int {
	n := 0
	for _, p := range fb.Pixels {
		if p != 0 {
			n++
		}
	}
	return n
}

type liveCtl struct {
	ch     *rc.Channel
	fb     *rc.Framebuffer
	cipher int
	frames int
	rc.NopController
}

func (c *liveCtl) SetCipher(cipher int) {
	c.cipher = cipher
	fmt.Println("SetCipher", cipher)
	_ = c.ch.SetCipher(cipher)
}
func (c *liveCtl) SendAck()             { fmt.Println("Ack"); _ = c.ch.Send(rc.Ack()) }
func (c *liveCtl) RefreshScreen()       { fmt.Println("Refresh"); _ = c.ch.Send(rc.RefreshScreen()) }
func (c *liveCtl) OnLicensed(f int)     { fmt.Println("Lic", f) }
func (c *liveCtl) OnFlags(f int)        { fmt.Println("Flags", f) }
func (c *liveCtl) OnStatus(f int, t string) {
	fmt.Println("Status", f, t)
}
func (c *liveCtl) OnResize(w, h int) { fmt.Println("Resize", w, h) }
func (c *liveCtl) OnFrame() {
	c.frames++
	if c.frames <= 5 {
		fmt.Println("Frame", c.frames, c.fb.Width, c.fb.Height, "nz", nonzero(c.fb))
	}
}
func (c *liveCtl) OnPower(on bool) { fmt.Println("Power", on) }
func (c *liveCtl) OnExit()         { fmt.Println("Exit") }
