// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// handshakeTimeout bounds how long a sandbox may take to tell us where it wants
	// to go. A connection that opens and says nothing holds a goroutine and a socket,
	// so it is not allowed to do so indefinitely.
	handshakeTimeout = 10 * time.Second

	// idleTimeout bounds how long a keep-alive connection may sit between requests.
	idleTimeout = 90 * time.Second

	// dialTimeout bounds the connection to the upstream host once it is allowed.
	dialTimeout = 15 * time.Second
)

// Proxy decides, per sandbox, which hosts an outbound connection may reach.
//
// SCOPE OF THE GUARANTEE, stated precisely so it is not over-read:
//
//   - Cleartext HTTP is authorized PER REQUEST. Every request on a keep-alive
//     connection is parsed and checked, so a later request cannot change Host to
//     somewhere unapproved.
//
//   - HTTPS is authorized PER CONNECTION, on the TLS server name, and is then
//     forwarded without decryption. That is weaker, and the difference matters:
//     one TLS connection to an approved host can carry requests for other
//     authorities the SNI never mentions (HTTP/2 coalescing, RFC 9113 §9.1.1), and
//     an approved host can itself relay or store data. This is a control on which
//     endpoints a sandbox may reach -- not on what it may say once connected.
//     Strict per-request HTTPS enforcement needs a TLS-terminating broker, which
//     this is deliberately not.
//
// Anything without a readable destination name is refused rather than guessed at.
type Proxy struct {
	log       *slog.Logger
	registry  *Registry
	bindAddr  string
	httpPort  int
	httpsPort int

	listeners []net.Listener
	closeOnce sync.Once
	closed    chan struct{}

	// allowPrivateUpstreams is false everywhere except tests, which necessarily
	// dial a loopback listener. See dial().
	allowPrivateUpstreams bool

	// dialUpstream is the seam tests use to stand in for the real internet. In
	// production it is nil and dial() runs; a test sets it to reach a local
	// listener without having to own a public hostname or port 443.
	dialUpstream func(host string, port int) (net.Conn, error)
}

// New builds a Proxy that will listen on bindAddr once Start is called.
//
// bindAddr is the sandbox bridge gateway, not the wildcard address. The runner also
// has a VPC interface, and a proxy bound to 0.0.0.0 would be an open forwarder
// reachable by anything that can route to the runner -- it applies whatever policy
// is registered for the *source address*, so a neighbour arriving from off-box would
// be refused, but exposing the listener at all is a needless attack surface.
func New(logger *slog.Logger, registry *Registry, bindAddr string, httpPort, httpsPort int) *Proxy {
	return &Proxy{
		log:       logger.With(slog.String("component", "egress_proxy")),
		registry:  registry,
		bindAddr:  bindAddr,
		httpPort:  httpPort,
		httpsPort: httpsPort,
		closed:    make(chan struct{}),
	}
}

// Start binds both listeners and serves them until Stop.
//
// Binding happens synchronously so a port conflict is reported to the caller at
// startup. A proxy that failed to bind while sandboxes were being redirected to it
// would black-hole their traffic, so the runner treats this error as fatal.
func (p *Proxy) Start() error {
	for _, spec := range []struct {
		port  int
		isTLS bool
	}{
		{p.httpsPort, true},
		{p.httpPort, false},
	} {
		ln, err := net.Listen("tcp", net.JoinHostPort(p.bindAddr, strconv.Itoa(spec.port)))
		if err != nil {
			p.Stop()
			return fmt.Errorf("listen on %s:%d: %w", p.bindAddr, spec.port, err)
		}
		p.listeners = append(p.listeners, ln)
		go p.serve(ln, spec.isTLS)
	}

	p.log.Info("Egress proxy started",
		"bindAddr", p.bindAddr, "httpPort", p.httpPort, "httpsPort", p.httpsPort)
	return nil
}

// Stop closes the listeners. In-flight connections drain on their own.
func (p *Proxy) Stop() {
	p.closeOnce.Do(func() {
		close(p.closed)
		for _, ln := range p.listeners {
			_ = ln.Close()
		}
	})
}

func (p *Proxy) serve(ln net.Listener, isTLS bool) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-p.closed:
				return
			default:
			}
			p.log.Error("Egress accept failed", "error", err)
			return
		}
		go p.handle(conn, isTLS)
	}
}

func (p *Proxy) handle(client net.Conn, isTLS bool) {
	defer client.Close()

	sandboxIP, _, err := net.SplitHostPort(client.RemoteAddr().String())
	if err != nil {
		return
	}

	policy, ok := p.registry.For(sandboxIP)
	if !ok {
		// Redirected here without a policy. That should be impossible -- the
		// iptables redirect and the policy are installed together -- so it means
		// the two have drifted, and the safe reading of "I do not know what this
		// sandbox may reach" is "nothing".
		p.log.Warn("Egress refused: no policy for source", "sandboxIp", sandboxIP)
		return
	}

	if isTLS {
		p.handleTLS(client, sandboxIP, policy)
		return
	}
	p.handleHTTP(client, sandboxIP, policy)
}

// handleTLS authorizes on the TLS server name and then forwards bytes untouched.
func (p *Proxy) handleTLS(client net.Conn, sandboxIP string, policy Policy) {
	if err := client.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return
	}

	hello, host, err := readClientHello(client)
	if err != nil {
		p.log.Warn("Egress denied: unreadable TLS destination",
			"sandboxIp", sandboxIP, "reason", err.Error())
		return
	}

	host = normalizeHost(host)
	if !Allowed(host, policy.Patterns) {
		// TLS cannot carry a policy explanation before the handshake completes, so
		// the denial is recorded here and the client only sees the connection go
		// away. That asymmetry with HTTP (which does get a 403) is inherent.
		p.log.Warn("Egress denied by allow list", "sandboxIp", sandboxIP, "host", host,
			"proto", "tls", "revision", policy.Revision, "allowed", policy.Patterns)
		return
	}

	upstream, err := p.dialFor(host, 443)
	if err != nil {
		p.log.Info("Egress allowed but upstream unreachable",
			"sandboxIp", sandboxIP, "host", host, "reason", err.Error())
		return
	}
	defer upstream.Close()

	if err := client.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	// Replay the ClientHello exactly once, only now that it is authorized.
	if _, err := upstream.Write(hello); err != nil {
		return
	}

	p.log.Info("Egress allowed", "sandboxIp", sandboxIP, "host", host, "proto", "tls")
	splice(client, upstream)
}

// handleHTTP authorizes every request on the connection, not just the first.
//
// Reading one Host header and then splicing the socket would be a hole, not a
// shortcut: HTTP/1.1 keep-alive lets a client send a second request with a different
// Host down the same connection, and a spliced proxy would forward it unexamined.
// The request is therefore parsed with net/http -- which owns framing, and rejects
// the ambiguous Content-Length/Transfer-Encoding combinations that request smuggling
// relies on -- and each one is checked before it is forwarded.
func (p *Proxy) handleHTTP(client net.Conn, sandboxIP string, policy Policy) {
	transport := &http.Transport{
		// Explicitly nil: net/http would otherwise consult HTTP_PROXY from the
		// runner's environment and hand our egress to an upstream proxy that is no
		// part of this design.
		Proxy:                 nil,
		DialContext:           p.transportDial,
		ForceAttemptHTTP2:     false,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       idleTimeout,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	// This transport is per-connection and therefore per-sandbox: no pooled upstream
	// connection is ever shared between two sandboxes or survives a policy change.
	defer transport.CloseIdleConnections()

	reader := bufio.NewReader(client)

	for {
		if err := client.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}

		req, err := http.ReadRequest(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				p.log.Info("Egress HTTP read failed", "sandboxIp", sandboxIP, "reason", err.Error())
			}
			return
		}

		// CONNECT would turn this listener into an opaque tunnel, and the HTTP/2
		// cleartext preface (method PRI) would move framing somewhere this code
		// does not inspect. Both are refused rather than half-supported.
		if req.Method == http.MethodConnect || req.Method == "PRI" {
			writeStatus(client, http.StatusMethodNotAllowed, "method not permitted by egress policy")
			return
		}
		if req.Header.Get("Upgrade") != "" {
			writeStatus(client, http.StatusForbidden, "connection upgrades are not permitted by egress policy")
			return
		}

		host := normalizeHost(req.Host)
		if !Allowed(host, policy.Patterns) {
			p.log.Warn("Egress denied by allow list", "sandboxIp", sandboxIP, "host", host,
				"proto", "http", "revision", policy.Revision, "allowed", policy.Patterns)
			// A policy denial is reported as a policy denial, so the caller can tell
			// it apart from a DNS failure or an unreachable host.
			writeStatus(client, http.StatusForbidden, "host not permitted by egress policy: "+host)
			return
		}

		if err := client.SetReadDeadline(time.Time{}); err != nil {
			return
		}

		// RoundTrip does not follow redirects, which is what we want: a 3xx goes
		// back to the sandbox so its next request is authorized on its own merits
		// rather than the gateway silently chasing it somewhere unapproved.
		req.RequestURI = ""
		req.URL.Scheme = "http"
		req.URL.Host = req.Host

		resp, err := transport.RoundTrip(req)
		if err != nil {
			p.log.Info("Egress allowed but upstream failed",
				"sandboxIp", sandboxIP, "host", host, "reason", err.Error())
			writeStatus(client, http.StatusBadGateway, "upstream request failed")
			return
		}

		p.log.Info("Egress allowed",
			"sandboxIp", sandboxIP, "host", host, "proto", "http", "status", resp.StatusCode)

		writeErr := resp.Write(client)
		closeAfter := resp.Close || req.Close
		_ = resp.Body.Close()
		if writeErr != nil || closeAfter {
			return
		}
	}
}

func writeStatus(w io.Writer, code int, message string) {
	_, _ = fmt.Fprintf(w,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(message)+1, message+"\n")
}

// transportDial adapts the vetted dialer to net/http's DialContext signature.
//
// The address net/http passes is derived from the URL we just authorized, but it is
// re-resolved and re-vetted here rather than trusted: this is the only place the
// upstream socket is actually created, so it is the only place the address check is
// guaranteed to run.
func (p *Proxy) transportDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	return p.dialFor(normalizeHost(host), port)
}

func (p *Proxy) dialFor(host string, port int) (net.Conn, error) {
	if p.dialUpstream != nil {
		return p.dialUpstream(host, port)
	}
	return p.dial(host, port)
}

// dial connects to the host the client named, resolving it here rather than reusing
// the address the client was headed for.
//
// This is the load-bearing half of the check. If we authorized on the SNI but
// connected to the client's original destination, a sandbox could open a socket to
// any address it liked and put an allowed name in the ClientHello -- the policy would
// pass and the traffic would go somewhere else entirely. SNI is routing information
// the client chooses, not authentication. Resolving the approved name ourselves means
// the host we checked is the host we reach.
func (p *Proxy) dial(host string, port int) (net.Conn, error) {
	if ip := net.ParseIP(host); ip != nil {
		// An IP literal names no host, so there is nothing the allow list could have
		// approved. Refused even if the literal somehow matched a pattern.
		return nil, fmt.Errorf("destination %q is an address, not a hostname", host)
	}

	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}

	var lastErr error
	for _, ip := range addrs {
		if !p.allowPrivateUpstreams && !isPublic(ip) {
			// An allowed name that resolves inside the infrastructure is how a
			// sandbox would reach the instance metadata service, the database, or a
			// neighbouring sandbox -- none of which the operator meant to permit by
			// naming a public host. DNS is attacker-influenced, so the check is
			// against the resolved address, and it runs on every new connection so a
			// rebinding answer cannot slip through on a later dial.
			lastErr = fmt.Errorf("%s resolves to non-public address %s", host, ip)
			continue
		}

		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)), dialTimeout)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = errors.New("no addresses")
	}
	return nil, lastErr
}

// isPublic reports whether an address is one a sandbox may be forwarded to. It
// covers IPv4 and IPv6, and IPv4-mapped IPv6 forms, because "::ffff:169.254.169.254"
// must be judged as the metadata address it is.
func isPublic(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}

	if v4 := ip.To4(); v4 != nil {
		switch {
		// Carrier-grade NAT, where cloud providers place internal endpoints that
		// are technically not RFC1918.
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
			return false
		// 0.0.0.0/8, 192.0.0.0/24 (IETF protocol assignments), 198.18/15
		// (benchmarking), 240/4 (reserved) and the broadcast address.
		case v4[0] == 0,
			v4[0] == 192 && v4[1] == 0 && v4[2] == 0,
			v4[0] == 198 && (v4[1] == 18 || v4[1] == 19),
			v4[0] >= 240,
			v4.Equal(net.IPv4bcast):
			return false
		}
		return true
	}

	// IPv6 unique-local (fc00::/7). IPv6 is disabled on the runner today, so this
	// is belt-and-braces rather than the primary control.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return ip.IsGlobalUnicast()
}

// splice copies in both directions until either side is done.
func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Half-close so the peer sees EOF and can finish its own direction, rather
		// than both sides waiting on a connection neither will write to again.
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}

	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
}
