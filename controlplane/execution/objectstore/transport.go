package objectstore

import (
	"context"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Socket deadlines bound periods without network progress. Progress in either
// direction refreshes the shared deadline: HTTP may already be waiting for the
// response while the request body is still being uploaded. Client.Timeout stays
// zero because it would bound the entire transfer rather than idle time.
type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleConn) touch() error { return c.Conn.SetDeadline(time.Now().Add(c.timeout)) }
func (c *idleConn) Read(p []byte) (int, error) {
	if e := c.touch(); e != nil {
		return 0, e
	}
	n, e := c.Conn.Read(p)
	if n > 0 {
		c.touch()
	}
	return n, e
}
func (c *idleConn) Write(p []byte) (int, error) {
	if e := c.touch(); e != nil {
		return 0, e
	}
	socket, ok := c.Conn.(syscall.Conn)
	if !ok {
		n, e := c.Conn.Write(p)
		if n > 0 {
			c.touch()
		}
		return n, e
	}
	raw, e := socket.SyscallConn()
	if e != nil {
		return 0, e
	}
	total := 0
	for total < len(p) {
		written := 0
		var writeError error
		e = raw.Write(func(fd uintptr) bool {
			for {
				written, writeError = syscall.Write(int(fd), p[total:])
				if writeError == syscall.EINTR {
					continue
				}
				break
			}
			if writeError == syscall.EAGAIN || writeError == syscall.EWOULDBLOCK {
				writeError = nil
				return false
			}
			return true
		})
		if written > 0 {
			total += written
			c.touch()
		}
		if e != nil {
			return total, e
		}
		if writeError != nil {
			return total, writeError
		}
		if written == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}
func newIdleHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, e := dialer.DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &idleConn{conn, timeout}, nil
	}, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, ExpectContinueTimeout: timeout, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, DisableCompression: true}
	return &http.Client{Transport: transport}
}
