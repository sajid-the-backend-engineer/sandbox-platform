// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEveryRequestOnAKeepAliveConnectionIsAuthorized(t *testing.T) {
	// Reading one Host header and then splicing would forward this second request
	// unexamined. It is the difference between checking a connection and checking
	// the requests carried on it, and HTTP/1.1 keep-alive makes that difference
	// reachable by any client that wants it.
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.registry.Register("127.0.0.1", Policy{Patterns: []string{"pypi.org"}, Revision: "test"})

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET /a HTTP/1.1\r\nHost: pypi.org\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(conn)
	first, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("first (allowed) request failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	// Same connection, different Host. It must be judged on its own merits.
	if _, err := conn.Write([]byte("GET /b HTTP/1.1\r\nHost: example.com\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	second, err := http.ReadResponse(br, nil)
	if err != nil {
		// Closing the connection on denial is also an acceptable refusal.
		return
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Errorf("second request status = %d, want 403", second.StatusCode)
	}
}

func TestTunnelsAndUpgradesAreRefused(t *testing.T) {
	// CONNECT would turn this listener into an opaque tunnel and h2c would move
	// framing somewhere this code does not inspect. Both are refused rather than
	// half-supported, because a half-supported tunnel is an unchecked one.
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.registry.Register("127.0.0.1", Policy{Patterns: []string{"pypi.org"}, Revision: "test"})

	for name, raw := range map[string]string{
		"connect": "CONNECT pypi.org:443 HTTP/1.1\r\nHost: pypi.org\r\n\r\n",
		"h2c":     "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n",
		"upgrade": "GET / HTTP/1.1\r\nHost: pypi.org\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", httpAddr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			if _, err := conn.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}

			buf := make([]byte, 256)
			n, _ := conn.Read(buf)
			if strings.Contains(string(buf[:n]), "200 OK") {
				t.Errorf("%s was forwarded; got %q", name, buf[:n])
			}
		})
	}
}

// TestAnOversizedRequestHeadIsRefused covers the ceiling that was declared and never
// applied.
//
// maxHTTPHead existed from the first version of this proxy, but the switch from a
// hand-rolled parser to net/http dropped the only code that used it -- and net/http
// brought no replacement, because MaxHeaderBytes belongs to http.Server and this path
// speaks http.ReadRequest directly. A sandbox could therefore make the proxy buffer
// without limit by sending headers and never a blank line. The read deadline bounds
// how long that lasts, not how much arrives in the meantime.
func TestAnOversizedRequestHeadIsRefused(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	registry := NewRegistry(discardLogger())
	registry.Register("10.9.9.9", Policy{Patterns: []string{"allowed.test"}})
	proxy := New(discardLogger(), registry, "127.0.0.1", 0, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Closed when the handler gives up, so the writes below fail immediately
		// instead of blocking on an unbuffered pipe with no reader. Without this the
		// test measures its own write deadline and passes whether or not the ceiling
		// is applied.
		defer server.Close()
		proxy.handleHTTP(server, "10.9.9.9", Policy{Patterns: []string{"allowed.test"}})
	}()

	// A request head that never ends. Written in chunks so the test does not depend
	// on any single write reaching the proxy.
	_, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: allowed.test\r\n"))
	padding := "X-Pad: " + strings.Repeat("a", 1024) + "\r\n"

	var written int
	writeErr := error(nil)
	_ = client.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for written < 64*1024 && writeErr == nil {
		var n int
		n, writeErr = client.Write([]byte(padding))
		written += n
	}

	// The proxy must give up rather than keep buffering. Either it closed the
	// connection on us mid-write, or it returned once the budget was spent.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the proxy was still buffering after %d bytes of headers", written)
	}

	// The proxy has to stop near the ceiling, not merely stop eventually. The slack
	// covers the bufio read-ahead sitting above the limiter.
	if written > maxHTTPHead+8*1024 {
		t.Errorf("buffered %d bytes of request head; the %d-byte ceiling was not applied",
			written, maxHTTPHead)
	}
	t.Logf("the proxy gave up after %d bytes against a %d-byte ceiling", written, maxHTTPHead)
}
