// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"net"
	"testing"
)

// TestAnyHostPermitsNamesButNotAddresses is the whole point of the public-internet
// posture: a sandbox may reach any public site, and still cannot reach the
// infrastructure around it.
//
// The two halves are enforced in different places, which is why both are asserted
// here -- the name check stops caring, and the address check does not.
func TestAnyHostPermitsNamesButNotAddresses(t *testing.T) {
	public := []string{AnyHost}

	for _, host := range []string{"example.com", "api.stripe.com", "a.b.c.example.org"} {
		if !Allowed(host, public) {
			t.Errorf("Allowed(%q, public) = false, want true", host)
		}
	}

	// A connection that names nothing is still refused: there is no destination to
	// vet, and "any host" is not "no host".
	if Allowed("", public) {
		t.Error("an empty host was permitted under the public-internet posture")
	}

	// The addresses that matter are refused regardless of the name that resolved to
	// them -- this is the check that keeps metadata and internal services out of
	// reach when every hostname is allowed.
	for _, addr := range []string{
		"169.254.169.254", // cloud instance metadata
		"10.20.0.2",       // VPC resolver / internal services
		"172.17.0.5",      // a neighbouring sandbox
		"127.0.0.1",       // the runner itself
		"100.64.0.1",      // carrier-grade NAT
	} {
		if isPublic(net.ParseIP(addr)) {
			t.Errorf("isPublic(%s) = true; public-internet mode would reach it", addr)
		}
	}
}

func TestAnyHostIsRecognisedFromTheWireFormat(t *testing.T) {
	patterns := ParseAllowList("*")
	if !AllowsAnyHost(patterns) {
		t.Fatalf(`ParseAllowList("*") = %v, want the any-host posture`, patterns)
	}

	// A named list must not be mistaken for it.
	if AllowsAnyHost(ParseAllowList("pypi.org,files.pythonhosted.org")) {
		t.Error("a named allow list was read as public-internet")
	}
	// Nor should a wildcard subdomain pattern, which is a different thing entirely.
	if AllowsAnyHost(ParseAllowList("*.github.com")) {
		t.Error("*.github.com was read as public-internet")
	}
}

func TestAnyHostStillDeniesUnknownSandboxes(t *testing.T) {
	// Public-internet is a policy, not the absence of one. A source with no policy
	// registered is still refused -- the fail-closed rule does not change.
	reg := NewRegistry(discardLogger())
	if _, ok := reg.For("172.17.0.9"); ok {
		t.Error("an unregistered address reported a policy")
	}

	reg.Register("172.17.0.9", Policy{Patterns: ParseAllowList("*"), Revision: "test"})
	policy, ok := reg.For("172.17.0.9")
	if !ok || !AllowsAnyHost(policy.Patterns) {
		t.Error("public-internet policy did not round-trip through the registry")
	}
}
