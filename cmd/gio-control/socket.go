package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	socketProbeTimeout = 500 * time.Millisecond
	// defaultIdleTimeout drops a connection that sends nothing for this long, so
	// one idle client cannot hold the single-connection server forever.
	defaultIdleTimeout = 10 * time.Second
	// maxSocketPathLen is the Linux sun_path limit (108 bytes) minus the NUL.
	maxSocketPathLen = 107
)

// pidPath returns the file that records the serving process for socketPath.
func pidPath(socketPath string) string { return socketPath + ".pid" }

// validateSocketPath rejects paths that cannot be bound as a Unix socket.
func validateSocketPath(socketPath string) error {
	if socketPath == "" {
		return errors.New("socket path is empty")
	}
	if len(socketPath) > maxSocketPathLen {
		return fmt.Errorf("socket path %s is %d bytes, over the %d-byte Unix socket limit; use a shorter --socket (or GIO_DIR with scripts/gio)", socketPath, len(socketPath), maxSocketPathLen)
	}
	return nil
}

// serveSocket accepts connections on a Unix socket and answers them one at a
// time, since the driver is not safe for concurrent use. It returns nil after a
// shutdown request or when ctx is cancelled, removing the socket and pid file
// unless another server has replaced them. ready is called once the socket
// accepts connections.
func serveSocket(ctx context.Context, socketPath string, srv *server, ready func() error) error {
	if err := validateSocketPath(socketPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	socketInfo, err := os.Stat(socketPath)
	if err != nil {
		// The listener still unlinks the socket it bound when closed.
		_ = listener.Close()
		return fmt.Errorf("stat socket: %w", err)
	}
	// Remove the socket ourselves, and only if it is still ours: a restart may
	// already have bound a new server to the same path.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
	defer func() {
		_ = listener.Close()
		if info, err := os.Stat(socketPath); err == nil && os.SameFile(info, socketInfo) {
			_ = os.Remove(socketPath)
		}
		if current, err := os.ReadFile(pidPath(socketPath)); err == nil && bytes.Equal(current, pid) {
			_ = os.Remove(pidPath(socketPath))
		}
	}()
	if err := os.WriteFile(pidPath(socketPath), pid, 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	stopListening := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopListening()
	if ready != nil {
		if err := ready(); err != nil {
			return err
		}
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept connection: %w", err)
		}
		shutdown, err := serveConn(ctx, srv, conn)
		if err != nil && ctx.Err() == nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "connection: %v\n", err)
		}
		if shutdown || ctx.Err() != nil {
			return nil
		}
	}
}

func serveConn(ctx context.Context, srv *server, conn net.Conn) (bool, error) {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	idle := srv.idleTimeout
	if idle <= 0 {
		idle = defaultIdleTimeout
	}
	dc := deadlineConn{Conn: conn, timeout: idle}
	return srv.serve(ctx, dc, dc)
}

// deadlineConn refreshes the connection deadline before each read and write,
// so a peer that stops talking or reading is dropped after timeout.
type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c deadlineConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c deadlineConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

// removeStaleSocket deletes a socket left by a dead server and refuses to
// replace one that still accepts connections.
func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", socketPath)
	}
	if socketAccepts(socketPath) {
		return fmt.Errorf("a gio-control server is already running on %s", socketPath)
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

// socketAccepts reports whether a server accepts connections on socketPath.
func socketAccepts(socketPath string) bool {
	if validateSocketPath(socketPath) != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", socketPath, socketProbeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
