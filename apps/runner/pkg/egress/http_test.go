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
	p.Register("127.0.0.1", []string{"pypi.org"})

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
	p.Register("127.0.0.1", []string{"pypi.org"})

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
