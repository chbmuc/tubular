package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"code.cfops.it/sys/tubular/internal/log"
)

const (
	ioDeadline      = 30 * time.Second
	maxRequestBytes = 8
)

func serve(e *env, args ...string) error {
	set := e.newFlagSet("serve", "listen address")
	set.Description = `
	Listen for command requests on the given address.

	Listen address must be a pathname or an abstract Unix socket address.
	Addresses starting with an at symbol ("@") are treated as abstract addresses.

	Server uses a sequenced-packet socket (SOCK_SEQPACKET) to listen for requests.

	Examples:
	  $ tubectl serve /tmp/tubectl.sock
	  $ tubectl serve @tubectl`

	if err := set.Parse(args); err != nil {
		return err
	}

	if set.NArg() != 1 {
		set.Usage()
		return fmt.Errorf("expected listen address but got %d arguments: %w", set.NArg(), errBadArg)
	}
	listenAddress := set.Arg(0)

	unixAddr := resolveUnixAddr("unixpacket", listenAddress)
	if unixAddr == nil {
		set.Usage()
		return fmt.Errorf("invalid listen address %q: need pathname or abstract address: %w", listenAddress, errBadArg)
	}
	ln, err := net.ListenUnix(unixAddr.Network(), unixAddr)
	if err != nil {
		return fmt.Errorf("Listen(%v) error: %w", unixAddr, err)
	}

	// Accept-loop interrupter
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			// Log all unexpected errors
			if !errors.Is(err, net.ErrClosed) {
				e.stderr.Logf("Accept(%v) error: %v", ln.Addr(), err)
			}

			// Treat max FDs error as not temporary
			if errors.Is(err, syscall.EMFILE) {
				break
			}
			// Retry on temporary/timeout errors
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			break
		}

		// TODO: Recover from panics in goroutine
		wg.Add(1)
		go serveConn(ctx, &wg, e.stderr, conn)
	}
	wg.Wait()

	if err != nil {
		return fmt.Errorf("Accept(%v) error: %w", ln.Addr(), err)
	}
	return nil
}

// Convert Unix socket address from presentation to network format.
//
// Compared to net.ResolveUnixAddress, this helper:
// - rejects empty address strings,
// - returns nil instead of net.UnknownNetworkError for invalid networks.
func resolveUnixAddr(network, address string) *net.UnixAddr {
	if len(address) == 0 || address == "@" {
		return nil
	}
	ua, err := net.ResolveUnixAddr(network, address)
	if err != nil {
		return nil
	}
	return ua

}

func serveConn(ctx context.Context, wg *sync.WaitGroup, errorLog log.Logger, conn *net.UnixConn) {
	defer wg.Done()

	err := serveLoop(ctx, conn)
	if err != nil {
		errorLog.Logf("serve error: %v", err)
	}
}

func serveLoop(ctx context.Context, conn *net.UnixConn) error {
	// Read-Write loop interrupter
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		var (
			req  = make([]byte, maxRequestBytes)
			resp []byte
		)

		err := conn.SetReadDeadline(time.Now().Add(ioDeadline))
		if err != nil {
			return fmt.Errorf("SetReadDeadline: %w", err)
		}
		// TODO: Detect truncated reads.
		n, err := conn.Read(req)
		if err != nil {
			if isClosedOrTimeout(err) {
				return nil // expected
			}
			return fmt.Errorf("Read: %w", err)
		}

		resp = handleRequest(req[:n])

		err = conn.SetWriteDeadline(time.Now().Add(ioDeadline))
		if err != nil {
			return fmt.Errorf("SetWriteDeadline: %w", err)
		}
		_, err = conn.Write(resp)
		if err != nil {
			if isClosedOrTimeout(err) {
				return nil // expected
			}
			return fmt.Errorf("Write: %v", err)
		}
	}
}

func isClosedOrTimeout(e error) bool {
	return errors.Is(e, net.ErrClosed) || errors.Is(e, io.EOF) || os.IsTimeout(e)
}

func handleRequest(req []byte) []byte {
	if bytes.Equal(req, []byte("version")) {
		// TODO: Delegate to command handler
		return []byte(Version)
	}
	return []byte("error")
}
