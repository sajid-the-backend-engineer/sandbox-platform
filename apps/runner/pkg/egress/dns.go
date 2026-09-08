// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WHY A RESOLVER LIVES HERE.
//
// The first draft of this package assumed sandbox DNS never crossed the firewall,
// because Docker answers name lookups itself at 127.0.0.11. That is true only on
// user-defined networks. These sandboxes run on Docker's DEFAULT bridge, where the
// host's resolv.conf is copied into the container verbatim -- measured on the
// production runner, a sandbox asks 10.20.0.2 directly. Those are ordinary forwarded
// packets, so the default-deny rule would have broken every lookup, and an exception
// for port 53 would have handed back an uncontrolled channel: a resolver that
// answers anything is a way to reach names the allow list refuses, and DNS queries
// carry data outward whether or not a connection follows.
//
// So DNS is redirected here instead. Queries are attributed to a sandbox by source
// address, checked against the same allow list the proxy uses, and only then
// forwarded upstream.
//
// WHAT THIS DOES NOT CLAIM. This is not zero DNS exfiltration. A policy permitting
// "*.example.com" permits a query for "<data>.example.com", and an approved host can
// relay. The guarantee is narrower and worth stating exactly: a restricted sandbox
// cannot resolve names outside its allow list, and cannot reach any resolver except
// this one.

const (
	// maxDNSMessage bounds a single query. 4096 covers EDNS0 advertised sizes; a
	// larger claim is refused rather than buffered.
	maxDNSMessage = 4096

	// dnsUpstreamTimeout bounds the forwarded query.
	dnsUpstreamTimeout = 5 * time.Second

	// rcodeRefused is the DNS response code for a policy refusal. It is deliberately
	// not NXDOMAIN: "this name does not exist" would be a lie, and a resolver that
	// lies is one nobody can debug. REFUSED says the answer was withheld.
	rcodeRefused = 5

	// rcodeFormatError is returned for a query this resolver cannot parse.
	rcodeFormatError = 1
)

// Resolver answers DNS for restricted sandboxes, subject to their allow list.
type Resolver struct {
	log      *slog.Logger
	registry *Registry
	bindAddr string
	port     int
	upstream string

	udp       *net.UDPConn
	tcp       net.Listener
	closeOnce sync.Once
	closed    chan struct{}
}

// NewResolver builds a Resolver. upstream is the address queries are forwarded to
// once allowed, in host:port form.
func NewResolver(logger *slog.Logger, registry *Registry, bindAddr string, port int, upstream string) *Resolver {
	return &Resolver{
		log:      logger.With(slog.String("component", "egress_dns")),
		registry: registry,
		bindAddr: bindAddr,
		port:     port,
		upstream: upstream,
		closed:   make(chan struct{}),
	}
}

// Start binds the UDP and TCP listeners.
//
// Both are required. A resolver that only spoke UDP would leave TCP/53 to be either
// dropped -- breaking large answers and any client that retries over TCP -- or
// allowed out, which would be the uncontrolled path this exists to close.
func (r *Resolver) Start() error {
	addr := net.JoinHostPort(r.bindAddr, strconv.Itoa(r.port))

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve dns bind address: %w", err)
	}
	r.udp, err = net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", addr, err)
	}

	r.tcp, err = net.Listen("tcp", addr)
	if err != nil {
		_ = r.udp.Close()
		return fmt.Errorf("listen tcp %s: %w", addr, err)
	}

	go r.serveUDP()
	go r.serveTCP()

	r.log.Info("Egress resolver started", "bindAddr", r.bindAddr, "port", r.port, "upstream", r.upstream)
	return nil
}

func (r *Resolver) Stop() {
	r.closeOnce.Do(func() {
		close(r.closed)
		if r.udp != nil {
			_ = r.udp.Close()
		}
		if r.tcp != nil {
			_ = r.tcp.Close()
		}
	})
}

func (r *Resolver) serveUDP() {
	buf := make([]byte, maxDNSMessage)
	for {
		n, from, err := r.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.closed:
				return
			default:
			}
			continue
		}

		query := make([]byte, n)
		copy(query, buf[:n])
		go func(query []byte, from *net.UDPAddr) {
			resp := r.answer(query, from.IP.String())
			if resp != nil {
				_, _ = r.udp.WriteToUDP(resp, from)
			}
		}(query, from)
	}
}

func (r *Resolver) serveTCP() {
	for {
		conn, err := r.tcp.Accept()
		if err != nil {
			select {
			case <-r.closed:
				return
			default:
			}
			return
		}
		go r.handleTCP(conn)
	}
}

func (r *Resolver) handleTCP(conn net.Conn) {
	defer conn.Close()

	sandboxIP, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return
	}
	if err := conn.SetDeadline(time.Now().Add(dnsUpstreamTimeout * 2)); err != nil {
		return
	}

	// DNS over TCP frames each message with a two-byte length prefix.
	var length uint16
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return
	}
	if length == 0 || int(length) > maxDNSMessage {
		return
	}

	query := make([]byte, length)
	if _, err := io.ReadFull(conn, query); err != nil {
		return
	}

	resp := r.answer(query, sandboxIP)
	if resp == nil {
		return
	}
	if err := binary.Write(conn, binary.BigEndian, uint16(len(resp))); err != nil {
		return
	}
	_, _ = conn.Write(resp)
}

// answer decides a single query and returns the bytes to send back.
func (r *Resolver) answer(query []byte, sandboxIP string) []byte {
	name, err := questionName(query)
	if err != nil {
		r.log.Info("DNS query unparseable", "sandboxIp", sandboxIP, "reason", err.Error())
		return errorResponse(query, rcodeFormatError)
	}

	policy, ok := r.registry.For(sandboxIP)
	if !ok {
		// The firewall sent us a sandbox we have no policy for. Refuse: an unknown
		// sandbox is not an unrestricted one.
		r.log.Warn("DNS refused: no policy for source", "sandboxIp", sandboxIP, "name", name)
		return errorResponse(query, rcodeRefused)
	}

	if !Allowed(name, policy.Patterns) {
		r.log.Warn("DNS refused by allow list",
			"sandboxIp", sandboxIP, "name", name, "revision", policy.Revision)
		return errorResponse(query, rcodeRefused)
	}

	// A public-internet policy resolves any name, but the answer still buys nothing
	// on its own: connections are dialled by the proxy, which vets the resolved
	// address. Resolution and reachability are separate gates on purpose.

	resp, err := r.forward(query)
	if err != nil {
		r.log.Info("DNS upstream failed",
			"sandboxIp", sandboxIP, "name", name, "reason", err.Error())
		// A server failure, reported as one. Distinguishable from the refusal above
		// so an operator can tell a policy denial from a broken resolver.
		return errorResponse(query, 2) // SERVFAIL
	}

	r.log.Info("DNS allowed", "sandboxIp", sandboxIP, "name", name, "revision", policy.Revision)
	return resp
}

// forward relays an approved query to the upstream resolver.
func (r *Resolver) forward(query []byte) ([]byte, error) {
	conn, err := net.DialTimeout("udp", r.upstream, dnsUpstreamTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(dnsUpstreamTimeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}

	buf := make([]byte, maxDNSMessage)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// questionName extracts the QNAME from a DNS query.
//
// Only the first question is read. Multi-question queries are not used in practice
// and answering one while ignoring another would be a way to smuggle a name past the
// check, so anything other than exactly one question is rejected.
func questionName(msg []byte) (string, error) {
	if len(msg) < 12 {
		return "", errors.New("message shorter than a DNS header")
	}
	if qdcount := binary.BigEndian.Uint16(msg[4:6]); qdcount != 1 {
		return "", fmt.Errorf("expected exactly 1 question, got %d", qdcount)
	}

	var labels []string
	pos := 12
	for {
		if pos >= len(msg) {
			return "", io.ErrUnexpectedEOF
		}
		length := int(msg[pos])
		if length == 0 {
			break
		}
		// Compression pointers are not legal in a question section, and following
		// one would be a way to make the name we check differ from the name the
		// upstream resolver sees.
		if length&0xc0 != 0 {
			return "", errors.New("compression pointer in question name")
		}
		pos++
		if pos+length > len(msg) {
			return "", io.ErrUnexpectedEOF
		}
		labels = append(labels, string(msg[pos:pos+length]))
		pos += length
		if len(labels) > 127 {
			return "", errors.New("too many labels")
		}
	}

	name := strings.Join(labels, ".")
	if name == "" {
		return "", errors.New("empty question name")
	}
	if len(name) > 253 {
		return "", errors.New("name longer than 253 octets")
	}
	return normalizeHost(name), nil
}

// errorResponse builds a reply to query carrying the given RCODE and no answers.
func errorResponse(query []byte, rcode byte) []byte {
	if len(query) < 12 {
		return nil
	}

	resp := make([]byte, len(query))
	copy(resp, query)

	// QR=1 (response), keep OPCODE and RD, set RA, set RCODE.
	resp[2] = (query[2] & 0x7f) | 0x80
	resp[3] = (query[3] & 0xf0) | (rcode & 0x0f)
	resp[3] |= 0x80 // RA: recursion available

	// No answer, authority or additional records.
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)

	// Truncate anything after the question section so a stale EDNS0 OPT record from
	// the query is not echoed back as if it were ours.
	if end, err := questionEnd(query); err == nil && end <= len(resp) {
		resp = resp[:end]
	}
	return resp
}

// questionEnd returns the offset just past the first question.
func questionEnd(msg []byte) (int, error) {
	pos := 12
	for {
		if pos >= len(msg) {
			return 0, io.ErrUnexpectedEOF
		}
		length := int(msg[pos])
		if length == 0 {
			pos++
			break
		}
		if length&0xc0 != 0 {
			return 0, errors.New("compression pointer in question name")
		}
		pos += 1 + length
	}
	pos += 4 // QTYPE + QCLASS
	if pos > len(msg) {
		return 0, io.ErrUnexpectedEOF
	}
	return pos, nil
}

// UpstreamFromResolvConf reads the first nameserver out of a resolv.conf.
//
// The runner's own resolver is the upstream, so approved names resolve exactly as
// they would have without this component in the path. It is read at startup rather
// than hardcoded because it differs per deployment -- on the production runner it is
// the VPC resolver at 10.20.0.2, which no default could have guessed.
func UpstreamFromResolvConf(contents string) (string, error) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			if ip := net.ParseIP(fields[1]); ip != nil {
				return net.JoinHostPort(ip.String(), "53"), nil
			}
		}
	}
	return "", errors.New("no nameserver found in resolv.conf")
}
