// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dnsQuery builds a minimal A query for name.
func dnsQuery(name string) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:2], 0x1234) // ID
	binary.BigEndian.PutUint16(msg[2:4], 0x0100) // RD
	binary.BigEndian.PutUint16(msg[4:6], 1)      // QDCOUNT

	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0)                          // root label
	msg = binary.BigEndian.AppendUint16(msg, 1)   // QTYPE A
	msg = binary.BigEndian.AppendUint16(msg, 1)   // QCLASS IN
	return msg
}

func rcodeOf(t *testing.T, msg []byte) byte {
	t.Helper()
	if len(msg) < 12 {
		t.Fatalf("response too short: %d bytes", len(msg))
	}
	return msg[3] & 0x0f
}

func TestQuestionNameIsExtractedAndNormalized(t *testing.T) {
	name, err := questionName(dnsQuery("PyPI.org"))
	if err != nil {
		t.Fatalf("questionName: %v", err)
	}
	if name != "pypi.org" {
		t.Errorf("name = %q, want pypi.org", name)
	}
}

func TestQuestionNameRejectsMalformedQueries(t *testing.T) {
	// A compression pointer in the question would let the name we check differ from
	// the name the upstream resolver reads.
	pointer := dnsQuery("pypi.org")
	pointer[12] = 0xc0

	for name, msg := range map[string][]byte{
		"truncated":  {0x12, 0x34},
		"pointer":    pointer,
		"noQuestion": {0x12, 0x34, 0x01, 0x00, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		if _, err := questionName(msg); err == nil {
			t.Errorf("%s query was accepted; want an error", name)
		}
	}
}

// startResolver runs a Resolver whose upstream is a stub answering everything.
func startResolver(t *testing.T, reg *Registry) (*Resolver, string) {
	t.Helper()

	// A stub upstream that echoes the query back with QR set, so an allowed query
	// produces a recognisable NOERROR answer without touching the real internet.
	up, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = up.Close() })
	go func() {
		buf := make([]byte, maxDNSMessage)
		for {
			n, from, err := up.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := make([]byte, n)
			copy(resp, buf[:n])
			resp[2] |= 0x80 // QR
			resp[3] &^= 0x0f
			_, _ = up.WriteTo(resp, from)
		}
	}()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	r := NewResolver(discardLogger(), reg, "127.0.0.1", port, up.LocalAddr().String())
	if err := r.Start(); err != nil {
		t.Fatalf("resolver Start: %v", err)
	}
	t.Cleanup(r.Stop)

	return r, net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func askUDP(t *testing.T, addr string, query []byte) []byte {
	t.Helper()

	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, maxDNSMessage)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no DNS response: %v", err)
	}
	return buf[:n]
}

func TestResolverAnswersAllowedNamesAndRefusesOthers(t *testing.T) {
	reg := NewRegistry(discardLogger())
	reg.Register("127.0.0.1", Policy{Patterns: []string{"pypi.org"}, Revision: "test"})
	_, addr := startResolver(t, reg)

	if got := rcodeOf(t, askUDP(t, addr, dnsQuery("pypi.org"))); got != 0 {
		t.Errorf("allowed name rcode = %d, want 0 (NOERROR)", got)
	}

	// REFUSED, not NXDOMAIN: the name exists, we are declining to resolve it. A
	// resolver that claims a real name does not exist is one nobody can debug.
	if got := rcodeOf(t, askUDP(t, addr, dnsQuery("example.com"))); got != rcodeRefused {
		t.Errorf("denied name rcode = %d, want %d (REFUSED)", got, rcodeRefused)
	}
}

func TestResolverRefusesSandboxesWithNoPolicy(t *testing.T) {
	// An unknown sandbox is not an unrestricted one. This is the DNS half of the
	// same fail-closed rule the proxy applies.
	_, addr := startResolver(t, NewRegistry(discardLogger()))

	if got := rcodeOf(t, askUDP(t, addr, dnsQuery("pypi.org"))); got != rcodeRefused {
		t.Errorf("unregistered source rcode = %d, want %d (REFUSED)", got, rcodeRefused)
	}
}

func TestResolverAppliesWildcardsTheSameWayTheProxyDoes(t *testing.T) {
	reg := NewRegistry(discardLogger())
	reg.Register("127.0.0.1", Policy{Patterns: []string{"*.github.com"}, Revision: "test"})
	_, addr := startResolver(t, reg)

	if got := rcodeOf(t, askUDP(t, addr, dnsQuery("api.github.com"))); got != 0 {
		t.Errorf("wildcard subdomain rcode = %d, want 0", got)
	}
	// The apex is not covered by a wildcard, in the resolver exactly as in the proxy.
	if got := rcodeOf(t, askUDP(t, addr, dnsQuery("github.com"))); got != rcodeRefused {
		t.Errorf("apex rcode = %d, want %d (REFUSED)", got, rcodeRefused)
	}
}

func TestUpstreamFromResolvConf(t *testing.T) {
	// The shape actually found on the runner: a comment block, then the nameserver.
	conf := "# Generated by Docker Engine.\nnameserver 10.20.0.2\nsearch us-west-1.compute.internal\n"

	got, err := UpstreamFromResolvConf(conf)
	if err != nil {
		t.Fatalf("UpstreamFromResolvConf: %v", err)
	}
	if got != "10.20.0.2:53" {
		t.Errorf("upstream = %q, want 10.20.0.2:53", got)
	}

	if _, err := UpstreamFromResolvConf("search example.com\n"); err == nil {
		t.Error("a resolv.conf with no nameserver was accepted")
	}
}

func TestRevisionIsStableUnderReordering(t *testing.T) {
	a := Revision([]string{"pypi.org", "files.pythonhosted.org"})
	b := Revision([]string{"files.pythonhosted.org", "pypi.org"})
	if a != b {
		t.Errorf("revision changed under reordering: %s vs %s", a, b)
	}
	if a == Revision([]string{"pypi.org"}) {
		t.Error("revision did not change when the policy did")
	}
}
