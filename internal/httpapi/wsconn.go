package httpapi

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsNetConn adapts a gorilla WebSocket to net.Conn for RFB (binary frames only).
type wsNetConn struct {
	ws *websocket.Conn

	rmu sync.Mutex
	r   io.Reader

	wmu sync.Mutex
}

func newWSNetConn(ws *websocket.Conn) *wsNetConn {
	return &wsNetConn{ws: ws}
}

func (c *wsNetConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.r != nil {
			n, err := c.r.Read(p)
			if n > 0 {
				return n, nil
			}
			if err == io.EOF {
				c.r = nil
				continue
			}
			if err != nil {
				return 0, err
			}
		}
		mt, r, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}
		if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
			continue
		}
		c.r = r
	}
}

func (c *wsNetConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	w, err := c.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(p)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func (c *wsNetConn) Close() error                       { return c.ws.Close() }
func (c *wsNetConn) LocalAddr() net.Addr                { return c.ws.LocalAddr() }
func (c *wsNetConn) RemoteAddr() net.Addr               { return c.ws.RemoteAddr() }
func (c *wsNetConn) SetDeadline(t time.Time) error      { return c.ws.SetReadDeadline(t) }
func (c *wsNetConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsNetConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
