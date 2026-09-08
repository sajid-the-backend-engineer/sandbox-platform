// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"encoding/binary"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAllowedMatchesExactHostsOnly(t *testing.T) {
	patterns := []string{"pypi.org", "files.pythonhosted.org"}

	for _, host := range []string{"pypi.org", "PYPI.ORG", "pypi.org.", "pypi.org:443", "files.pythonhosted.org"} {
		if !Allowed(host, patterns) {
			t.Errorf("Allowed(%q) = false, want true", host)
		}
	}

	// The whole point of the defect being fixed: a host nobody listed is refused.
	for _, host := range []string{"example.com", "evil.com", ""} {
		if Allowed(host, patterns) {
			t.Errorf("Allowed(%q) = true, want false", host)
		}
	}
}

func TestABareEntryDoesNotCoverItsSubdomains(t *testing.T) {
	// A bare entry is exact. If this ever became a subtree match, every profile in
	// use would silently widen -- "github.com" would start permitting any host an
	// attacker could get published under it.
	if Allowed("evil.pypi.org", []string{"pypi.org"}) {
		t.Error("bare pattern matched a subdomain; allow lists must not widen implicitly")
	}
	// And the suffix must be on a label boundary, not a string boundary.
	if Allowed("notpypi.org", []string{"pypi.org"}) {
		t.Error("bare pattern matched a different domain sharing a suffix")
	}
}

func TestWildcardMatchesSubdomainsButNotTheParent(t *testing.T) {
	patterns := []string{"*.github.com"}

	for _, host := range []string{"api.github.com", "a.b.github.com"} {
		if !Allowed(host, patterns) {
			t.Errorf("Allowed(%q) = false, want true", host)
		}
	}
	if Allowed("github.com", patterns) {
		t.Error("wildcard matched the parent; list it explicitly when that is wanted")
	}
	if Allowed("evilgithub.com", patterns) {
		t.Error("wildcard matched across a label boundary")
	}
}

func TestParseAllowListIgnoresBlankEntries(t *testing.T) {
	got := ParseAllowList(" pypi.org , ,files.pythonhosted.org, ")
	want := []string{"pypi.org", "files.pythonhosted.org"}

	if len(got) != len(want) {
		t.Fatalf("ParseAllowList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseAllowList = %v, want %v", got, want)
		}
	}
}

func TestIsPublicRejectsInfrastructureAddresses(t *testing.T) {
	// Each of these is a way a sandbox could pivot inward using a name it was
	// legitimately allowed to reach. 169.254.169.254 is the one that matters most:
	// it is the cloud instance metadata service, and it hands out role credentials.
	for _, addr := range []string{
		"127.0.0.1", "10.0.1.5", "172.20.0.3", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0",
	} {
		if isPublic(net.ParseIP(addr)) {
			t.Errorf("isPublic(%s) = true, want false", addr)
		}
	}
	for _, addr := range []string{"1.1.1.1", "151.101.0.223", "2606:4700::1111"} {
		if !isPublic(net.ParseIP(addr)) {
			t.Errorf("isPublic(%s) = false, want true", addr)
		}
	}
}

// captureClientHello produces a genuine ClientHello for serverName by letting the
// standard library generate one, rather than hand-rolling bytes that might not match
// what a real client sends.
func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()

	ours, theirs := net.Pipe()
	go func() {
		_ = tls.Client(ours, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}).Handshake()
	}()
	defer ours.Close()
	defer theirs.Close()

	if err := theirs.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	n, err := theirs.Read(buf)
	if err != nil {
		t.Fatalf("reading ClientHello: %v", err)
	}
	return buf[:n]
}

func TestReadClientHelloExtractsServerName(t *testing.T) {
	hello := captureClientHello(t, "pypi.org")

	raw, sni, err := readClientHello(strings.NewReader(string(hello)))
	if err != nil {
		t.Fatalf("readClientHello: %v", err)
	}
	if sni != "pypi.org" {
		t.Errorf("sni = %q, want pypi.org", sni)
	}
	// The bytes must come back verbatim -- they are replayed to the real server, so
	// any mangling here would silently break the handshake.
	if string(raw) != string(hello) {
		t.Error("ClientHello was not returned byte-for-byte")
	}
}

func TestReadClientHelloRejectsNonTLS(t *testing.T) {
	if _, _, err := readClientHello(strings.NewReader("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Error("plain HTTP on the TLS port was accepted; want an error")
	}
}

func TestReadHTTPHeadExtractsHost(t *testing.T) {
	req := "GET /simple/ HTTP/1.1\r\nUser-Agent: pip/24\r\nHost: pypi.org\r\nAccept: */*\r\n\r\n"

	head, host, err := readHTTPHead(strings.NewReader(req))
	if err != nil {
		t.Fatalf("readHTTPHead: %v", err)
	}
	if host != "pypi.org" {
		t.Errorf("host = %q, want pypi.org", host)
	}
	if string(head) != req {
		t.Error("request head was not returned byte-for-byte")
	}
}

// startProxy runs a Proxy on ephemeral ports with upstream dialing redirected to a
// local echo server, and returns the two listener addresses.
func startProxy(t *testing.T, upstream net.Listener) (*Proxy, string, string) {
	t.Helper()

	// Ask the OS for two free ports, then hand them to the proxy.
	ports := make([]int, 0, 2)
	for i := 0; i < 2; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
		_ = l.Close()
	}

	p := New(discardLogger(), "127.0.0.1", ports[0], ports[1])
	p.dialUpstream = func(host string, port int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(p.Stop)

	return p, fmt.Sprintf("127.0.0.1:%d", ports[0]), fmt.Sprintf("127.0.0.1:%d", ports[1])
}

// echoServer accepts one connection, reads the request head, and replies.
func echoServer(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s",
					len(line), line)
			}(c)
		}
	}()

	return ln
}

func TestProxyForwardsAnAllowedHost(t *testing.T) {
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET /simple/ HTTP/1.1\r\nHost: pypi.org\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("allowed host did not get a response: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "200 OK") {
		t.Errorf("response = %q, want a 200 from upstream", buf[:n])
	}
}

func TestProxyRefusesAHostNotOnTheList(t *testing.T) {
	// This is the regression test for the reported defect: the sandbox declared
	// pypi.org and reached example.com anyway.
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// A cleartext denial is reported as a policy denial, so the caller can tell it
	// apart from DNS failure or an unreachable upstream.
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "403") {
		t.Fatalf("denied host got %q, want a 403 policy denial", got)
	}
	if strings.Contains(got, "200 OK") {
		t.Fatal("denied host reached upstream")
	}
}

func TestEveryRequestOnAKeepAliveConnectionIsAuthorized(t *testing.T) {
	// Splicing after the first Host header would forward this second request
	// unexamined. It is the difference between checking a connection and checking
	// the requests on it.
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET /a HTTP/1.1

Host: pypi.org



")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(conn)
	first, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	// Same connection, different Host -- must be refused on its own merits.
	if _, err := conn.Write([]byte("GET /b HTTP/1.1

Host: example.com



")); err != nil {
		t.Fatal(err)
	}
	second, err := http.ReadResponse(br, nil)
	if err != nil {
		return // connection closed on denial is also acceptable
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Errorf("second request status = %d, want 403", second.StatusCode)
	}
}

func TestConnectAndH2cAreRefused(t *testing.T) {
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})

	for _, raw := range []string{
		"CONNECT pypi.org:443 HTTP/1.1

Host: pypi.org



",
		"PRI * HTTP/2.0



SM



",
	} {
		conn, err := net.Dial("tcp", httpAddr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		if strings.Contains(string(buf[:n]), "200 OK") {
			t.Errorf("tunnel/upgrade attempt was forwarded: %q", raw)
		}
		conn.Close()
	}
}

func TestIPLiteralDestinationsAreRefused(t *testing.T) {
	// SNI is client-supplied routing information. A client can open a socket to any
	// address and name whatever it likes; dialing must go to the approved NAME.
	p := New(discardLogger(), "127.0.0.1", 0, 0)
	if _, err := p.dial("93.184.216.34", 443); err == nil {
		t.Error("dial accepted an IP literal as a hostname")
	}
}

func TestECHIsRefusedRatherThanTrustingTheOuterName(t *testing.T) {
	// A ClientHello whose real destination is encrypted cannot be authorized on the
	// name we can see, so the connection is refused.
	hello := captureClientHello(t, "pypi.org")
	withECH := injectExtension(t, hello, 0xfe0d, []byte{0x00, 0x01, 0x02})

	if _, _, err := readClientHello(strings.NewReader(string(withECH))); err == nil {
		t.Error("ClientHello carrying ECH was accepted; outer name is not authoritative")
	}
}

func TestProxyRefusesASandboxWithNoPolicy(t *testing.T) {
	upstream := echoServer(t)
	_, httpAddr, _ := startProxy(t, upstream) // deliberately no Register

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: pypi.org\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 256)
	if n, err := conn.Read(buf); err == nil && n > 0 {
		t.Fatalf("unregistered sandbox reached upstream: %q", buf[:n])
	}
}

func TestProxyDecidesTLSOnTheServerName(t *testing.T) {
	upstream := echoServer(t)
	p, _, httpsAddr := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})

	for _, tc := range []struct {
		name       string
		serverName string
		wantOpen   bool
	}{
		{"allowed", "pypi.org", true},
		{"denied", "example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", httpsAddr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			if _, err := conn.Write(captureClientHello(t, tc.serverName)); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}

			buf := make([]byte, 256)
			n, err := conn.Read(buf)
			gotOpen := err == nil && n > 0

			if gotOpen != tc.wantOpen {
				t.Errorf("connection open = %v, want %v (err=%v)", gotOpen, tc.wantOpen, err)
			}
		})
	}
}

func TestUnregisterFailsClosed(t *testing.T) {
	upstream := echoServer(t)
	p, httpAddr, _ := startProxy(t, upstream)
	p.Register("127.0.0.1", []string{"pypi.org"})
	p.Unregister("127.0.0.1")

	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: pypi.org\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 256)
	if n, err := conn.Read(buf); err == nil && n > 0 {
		t.Fatalf("policy survived Unregister: %q", buf[:n])
	}
}

// injectExtension appends a TLS extension to a captured ClientHello, fixing up the
// three nested length fields so the result is still well-formed.
//
// Built by hand because Go's TLS client will not emit ECH for us, and the property
// under test -- that a hello carrying ECH is refused rather than authorized on its
// visible outer name -- needs a hello that actually carries it.
func injectExtension(t *testing.T, hello []byte, extType uint16, body []byte) []byte {
	t.Helper()

	// Walk to the extensions_length field: 5 record header + 4 handshake header +
	// 2 version + 32 random, then three length-prefixed vectors.
	pos := 5 + 4 + 2 + 32
	pos += 1 + int(hello[pos])                                          // session_id
	pos += 2 + int(binary.BigEndian.Uint16(hello[pos:pos+2]))            // cipher_suites
	pos += 1 + int(hello[pos])                                          // compression_methods
	extLenAt := pos

	ext := make([]byte, 0, 4+len(body))
	ext = binary.BigEndian.AppendUint16(ext, extType)
	ext = binary.BigEndian.AppendUint16(ext, uint16(len(body)))
	ext = append(ext, body...)

	out := append(append([]byte{}, hello...), ext...)
	grow := uint16(len(ext))

	// extensions_length, then the 24-bit handshake length, then the record length.
	binary.BigEndian.PutUint16(out[extLenAt:], binary.BigEndian.Uint16(out[extLenAt:])+grow)
	hs := uint32(out[6])<<16 | uint32(out[7])<<8 | uint32(out[8])
	hs += uint32(grow)
	out[6], out[7], out[8] = byte(hs>>16), byte(hs>>8), byte(hs)
	binary.BigEndian.PutUint16(out[3:], binary.BigEndian.Uint16(out[3:])+grow)

	return out
}
