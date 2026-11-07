package cluster

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	linkReadLimit = 8 << 20
	// linkFrameSize caps HTTP/2 frames on links. No single write, and so no
	// message, carries much more than one frame, so this must stay well under
	// linkReadLimit.
	linkFrameSize = 1 << 20
)

// wsConn presents a WebSocket as the byte stream HTTP/2 runs on: every Write
// is one binary message and Read joins messages back into a stream.
type wsConn struct {
	ws *websocket.Conn

	readMu  sync.Mutex
	reader  io.Reader
	readErr error

	writeMu       sync.Mutex
	deadlineMu    sync.Mutex
	writeDeadline time.Time

	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
}

var _ net.Conn = (*wsConn)(nil)

func newWSConn(ws *websocket.Conn) *wsConn {
	ws.SetReadLimit(linkReadLimit)
	return &wsConn{ws: ws, done: make(chan struct{})}
}

func (c *wsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for c.readErr == nil {
		if c.reader == nil {
			kind, reader, err := c.ws.NextReader()
			if err != nil {
				c.failRead(err)
				break
			}
			if kind != websocket.BinaryMessage {
				c.failRead(errors.New("cluster link: unexpected text message"))
				break
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			err = nil
		}
		if err != nil {
			c.failRead(err)
			return n, c.readErr
		}
		if n > 0 {
			return n, nil
		}
	}
	return 0, c.readErr
}

// failRead makes the first read error permanent. gorilla panics after
// repeated reads on a failed connection, and a half-read message cannot be
// resumed anyway.
func (c *wsConn) failRead(err error) {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		err = io.EOF
	}
	c.readErr = err
	_ = c.Close()
}

func (c *wsConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// gorilla reads its write deadline without locking, so it is only set
	// while holding the write lock.
	c.deadlineMu.Lock()
	deadline := c.writeDeadline
	_ = c.ws.SetWriteDeadline(deadline)
	c.deadlineMu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		_ = c.Close()
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.ws.Close()
		close(c.done)
	})
	return c.closeErr
}

// Done is closed once the connection has failed or been closed.
func (c *wsConn) Done() <-chan struct{} { return c.done }

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// SetWriteDeadline also interrupts a write already blocked on the socket.
func (c *wsConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	err := c.ws.UnderlyingConn().SetWriteDeadline(t)
	c.deadlineMu.Unlock()
	return err
}
