// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

// Package egress enforces a sandbox's domain allow list.
//
// WHY THIS EXISTS. A domain allow list cannot be enforced with iptables. iptables
// matches addresses, and the names operators actually want to allow do not map to
// stable addresses: pypi.org and files.pythonhosted.org resolve into Fastly, whose
// address space also serves millions of unrelated sites. Allowing those addresses
// would let a sandbox reach any Fastly-hosted host (a leak), and pinning the
// addresses seen at create time would break the allowed host the first time the CDN
// moved (an outage). Both failures come from the same mistake: answering a question
// about names with a rule about numbers.
//
// So the name is read off the connection itself. Sandboxes on an allow list are
// default-deny at the packet layer and their TCP 80/443 is redirected to this proxy,
// which reads the destination the client asked for -- the TLS SNI, or the HTTP Host
// header -- decides, and then dials that name itself. Nothing is decrypted: SNI is
// cleartext in the ClientHello, so there is no interception, no certificate to
// manage, and no way for this to weaken TLS.
package egress

import "strings"

// normalizeHost reduces a hostname to the form the allow list is compared against:
// lowercase, no port, no trailing root dot, no surrounding whitespace.
//
// The port is stripped because an HTTP Host header carries one and SNI does not, and
// a policy written as "pypi.org" must cover both. The trailing dot is stripped
// because "pypi.org." is the same name in DNS but a different string here, and a
// fully-qualified name is exactly what an attacker would reach for if it slipped
// past the comparison.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))

	// Strip the port. An IPv6 literal is bracketed ("[::1]:443"), so a bare colon
	// count distinguishes "host:port" from an unbracketed IPv6 address.
	if strings.HasPrefix(h, "[") {
		if end := strings.Index(h, "]"); end != -1 {
			h = h[1:end]
		}
	} else if strings.Count(h, ":") == 1 {
		h = h[:strings.Index(h, ":")]
	}

	return strings.TrimSuffix(h, ".")
}

// Allowed reports whether host is permitted by patterns.
//
// A bare entry ("pypi.org") matches that name and nothing else -- NOT its
// subdomains. That is the least-privilege reading, and it is the one callers
// already write for: the profiles in use enumerate every host they need
// ("github.com", "codeload.github.com", "objects.githubusercontent.com") rather
// than relying on a parent to cover its children. Treating a bare entry as a
// subtree would silently widen every one of those policies.
//
// A subtree is opt-in with a leading "*.": "*.github.com" matches "api.github.com"
// and "a.b.github.com", but not "github.com" itself (list both when both are
// wanted). This is the same rule TLS certificates and browsers use, so it should
// hold no surprises -- except that ours also matches deeper labels, because an
// allow list is not a name-verification decision and "a.b.github.com" is no less
// under github.com's control than "api.github.com" is.
//
// An empty host is never allowed. That is the connection that offered no name at
// all -- a TLS client with no SNI, or an HTTP request with no Host -- and there is
// nothing to check it against.
func Allowed(host string, patterns []string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}

	for _, pattern := range patterns {
		p := normalizeHost(pattern)
		if p == "" {
			continue
		}

		if strings.HasPrefix(p, "*.") {
			// Match any deeper label under the parent, but not the parent itself.
			if suffix := p[1:]; strings.HasSuffix(h, suffix) && len(h) > len(suffix) {
				return true
			}
			continue
		}

		if h == p {
			return true
		}
	}

	return false
}

// ParseAllowList splits the comma-separated allow list carried on the API into
// individual patterns, dropping empties so a trailing comma or a stray space is not
// a policy change.
func ParseAllowList(list string) []string {
	var patterns []string
	for _, entry := range strings.Split(list, ",") {
		if e := normalizeHost(entry); e != "" {
			patterns = append(patterns, e)
		}
	}
	return patterns
}
