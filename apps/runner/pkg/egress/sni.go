// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// maxClientHello caps how much of a TLS record we will buffer while looking for the
// server name. A ClientHello is normally well under 2 KiB; the ceiling is here so a
// client cannot make the proxy hold memory by promising a record it never sends.
const maxClientHello = 16 * 1024

// maxHTTPHead caps the request head we buffer while looking for the Host header,
// for the same reason. Anything past this without a blank line is not a request we
// are willing to reason about.
//
// Enforced by headLimiter in proxy.go. It was declared here and left unwired when the
// hand-rolled HTTP path was replaced by net/http, and net/http does not bring a limit
// with it -- MaxHeaderBytes is a property of http.Server, which this path does not use.
const maxHTTPHead = 8 * 1024

// errHeadTooLarge marks a request head that ran past maxHTTPHead. It is a client
// error, not an upstream one, so it is reported separately from a transport failure.
var errHeadTooLarge = errors.New("request head exceeds the permitted size")

// headLimiter bounds how much a sandbox can make the proxy buffer before it has
// produced a complete request head.
//
// It sits UNDER the bufio.Reader, so the bound covers what is actually read from the
// socket rather than what the parser chose to ask for. Arming is per request, so a
// keep-alive connection gets a fresh budget for each one and a large body never
// consumes another request's allowance.
type headLimiter struct {
	src     io.Reader
	left    int
	limited bool
}

func (h *headLimiter) arm(n int) { h.limited, h.left = true, n }

func (h *headLimiter) disarm() { h.limited = false }

func (h *headLimiter) Read(p []byte) (int, error) {
	if !h.limited {
		return h.src.Read(p)
	}
	if h.left <= 0 {
		return 0, errHeadTooLarge
	}
	if len(p) > h.left {
		p = p[:h.left]
	}
	n, err := h.src.Read(p)
	h.left -= n
	return n, err
}

var errNoSNI = errors.New("no server name in ClientHello")

// errECH marks a ClientHello carrying the Encrypted ClientHello extension.
//
// ECH puts the real server name inside an encrypted inner hello; the SNI we can
// read is only the outer public name. Authorizing on the outer name would let a
// sandbox reach any host behind an allowed ECH-capable frontend, so this mode
// refuses the connection instead.
//
// COMPATIBILITY COST, stated plainly: clients that send GREASE ECH -- a decoy
// extension advertising support, which recent Chrome and Firefox do by default --
// are refused too, because a decoy is not distinguishable from the real thing
// without trying to decrypt it. curl, pip, npm and Go clients do not send it today.
// A browser inside a domain-restricted sandbox is therefore expected to fail.
var errECH = errors.New("ClientHello carries Encrypted ClientHello; outer name is not authoritative")

// extension type numbers we care about (RFC 6066 server_name, RFC 9849 ECH).
const (
	extServerName = 0x0000
	extECH        = 0xfe0d
)

// readClientHello reads one TLS record off r and returns both the exact bytes read
// and the server name inside it.
//
// The raw bytes are returned because this proxy does not terminate TLS -- it decides
// on the name, dials the real host, and replays this ClientHello verbatim so the
// handshake the client started completes with the server it asked for. Anything less
// faithful than a byte-for-byte replay would be a downgrade the client cannot see.
func readClientHello(r io.Reader) ([]byte, string, bool, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, "", false, fmt.Errorf("read TLS record header: %w", err)
	}

	// 0x16 is the handshake content type. Anything else on a port we redirected as
	// HTTPS is not TLS, and we will not guess at it.
	if header[0] != 0x16 {
		return header, "", false, fmt.Errorf("not a TLS handshake (content type 0x%02x)", header[0])
	}

	length := int(binary.BigEndian.Uint16(header[3:5]))
	if length == 0 || length > maxClientHello {
		return header, "", false, fmt.Errorf("TLS record length %d out of range", length)
	}

	record := make([]byte, 5+length)
	copy(record, header)
	if _, err := io.ReadFull(r, record[5:]); err != nil {
		return record, "", false, fmt.Errorf("read TLS record body: %w", err)
	}

	sni, err := parseSNI(record[5:])
	if errors.Is(err, errECH) {
		// Reported rather than refused here. Whether an encrypted inner name matters
		// depends on the policy: it is a bypass when a named allow list is being
		// enforced, and irrelevant when every public host is permitted anyway. The
		// caller knows which, this function does not.
		return record, sni, true, nil
	}
	return record, sni, false, err
}

// cursor is a bounds-checked reader over a byte slice. Every field in a ClientHello
// is attacker-controlled, so each read is length-checked and a short buffer is an
// error rather than a panic.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) take(n int) ([]byte, error) {
	if n < 0 || c.pos+n > len(c.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	out := c.buf[c.pos : c.pos+n]
	c.pos += n
	return out, nil
}

func (c *cursor) u8() (int, error) {
	b, err := c.take(1)
	if err != nil {
		return 0, err
	}
	return int(b[0]), nil
}

func (c *cursor) u16() (int, error) {
	b, err := c.take(2)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(b)), nil
}

// skipVector advances past a length-prefixed vector whose length field is lenBytes
// wide (1 or 2), which is the shape of every variable-length field in a ClientHello.
func (c *cursor) skipVector(lenBytes int) error {
	var n int
	var err error
	if lenBytes == 1 {
		n, err = c.u8()
	} else {
		n, err = c.u16()
	}
	if err != nil {
		return err
	}
	_, err = c.take(n)
	return err
}

// parseSNI walks a handshake message and returns the host_name from the
// server_name extension (RFC 6066).
func parseSNI(handshake []byte) (string, error) {
	var serverName string
	var sawECH bool
	c := &cursor{buf: handshake}

	msgType, err := c.u8()
	if err != nil {
		return "", err
	}
	if msgType != 0x01 {
		return "", fmt.Errorf("not a ClientHello (handshake type 0x%02x)", msgType)
	}

	// 24-bit handshake length, then client_version (2) and random (32).
	if _, err := c.take(3 + 2 + 32); err != nil {
		return "", err
	}
	if err := c.skipVector(1); err != nil { // session_id
		return "", err
	}
	if err := c.skipVector(2); err != nil { // cipher_suites
		return "", err
	}
	if err := c.skipVector(1); err != nil { // compression_methods
		return "", err
	}

	// Extensions are optional in the wire format; their absence means no SNI.
	extensionsLen, err := c.u16()
	if err != nil {
		return "", errNoSNI
	}
	extensions, err := c.take(extensionsLen)
	if err != nil {
		return "", err
	}

	e := &cursor{buf: extensions}
	for e.pos < len(e.buf) {
		extType, err := e.u16()
		if err != nil {
			return "", err
		}
		extLen, err := e.u16()
		if err != nil {
			return "", err
		}
		body, err := e.take(extLen)
		if err != nil {
			return "", err
		}

		if extType == extECH {
			sawECH = true
			continue
		}
		if extType != extServerName {
			continue
		}

		s := &cursor{buf: body}
		listLen, err := s.u16()
		if err != nil {
			return "", err
		}
		list, err := s.take(listLen)
		if err != nil {
			return "", err
		}

		l := &cursor{buf: list}
		for l.pos < len(l.buf) {
			nameType, err := l.u8()
			if err != nil {
				return "", err
			}
			nameLen, err := l.u16()
			if err != nil {
				return "", err
			}
			name, err := l.take(nameLen)
			if err != nil {
				return "", err
			}
			if nameType == 0x00 { // host_name
				// Recorded, not returned: the extension list is walked to the end
				// so an ECH extension appearing AFTER server_name still refuses the
				// connection. Returning early here would make the bypass depend on
				// extension ordering, which the client chooses.
				serverName = string(name)
			}
		}
	}

	if sawECH {
		// The outer name is returned alongside the flag so a public-internet policy
		// can still log where the connection went.
		return serverName, errECH
	}
	if serverName == "" {
		return "", errNoSNI
	}
	return serverName, nil
}

