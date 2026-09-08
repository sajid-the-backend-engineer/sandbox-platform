// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestEachFailureReportsItsOwnCause is about diagnosability, and it earns its place
// because the absence of it cost real time.
//
// Every one of these used to come back as "502 upstream request failed". A QA probe
// against cloud metadata and a probe against a genuinely broken site produced byte
// identical output, so neither result proved anything: the metadata denial could not
// be distinguished from the site being down, and a reviewer was right to refuse to
// mark the protection verified on that evidence.
//
// The rule these cases encode: a POLICY denial and an UPSTREAM failure have different
// owners. One means the sandbox asked for something it may not have; the other means
// something outside is broken. Sending a person to the wrong one wastes their day.
func TestEachFailureReportsItsOwnCause(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		wantStatus  int
		wantOutcome string
		wantSaysNot string // the message must rule out the OTHER explanation
	}{
		{
			name:        "bare IP address",
			err:         fmt.Errorf("%w: 1.1.1.1", errNotAHostname),
			wantStatus:  http.StatusForbidden,
			wantOutcome: "policy-denied-ip-literal",
			wantSaysNot: "hostname",
		},
		{
			name:        "resolves somewhere it may not reach",
			err:         fmt.Errorf("%w: metadata.test resolves to 169.254.169.254", errAddressNotPermitted),
			wantStatus:  http.StatusForbidden,
			wantOutcome: "policy-denied-address",
			wantSaysNot: "metadata",
		},
		{
			name:        "DNS could not answer",
			err:         fmt.Errorf("%w: nowhere.invalid: no such host", errResolveFailed),
			wantStatus:  http.StatusBadGateway,
			wantOutcome: "upstream-dns-failed",
			wantSaysNot: "not a policy denial",
		},
		{
			name:        "upstream refused the connection",
			err:         errors.New("dial tcp 93.184.216.34:443: connect: connection refused"),
			wantStatus:  http.StatusBadGateway,
			wantOutcome: "upstream-failed",
			wantSaysNot: "not a policy denial",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, message, outcome := classify(tc.err, "example.test")

			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tc.wantOutcome)
			}
			if !strings.Contains(message, tc.wantSaysNot) {
				t.Errorf("message %q does not mention %q", message, tc.wantSaysNot)
			}
		})
	}
}

// TestPolicyDenialsAndUpstreamFailuresNeverShareAStatus is the property the four cases
// above exist to guarantee, asserted directly so it cannot regress by someone adding a
// fifth case that quietly collapses back into 502.
func TestPolicyDenialsAndUpstreamFailuresNeverShareAStatus(t *testing.T) {
	policyDenials := []error{
		fmt.Errorf("%w: x", errNotAHostname),
		fmt.Errorf("%w: x", errAddressNotPermitted),
	}
	upstreamFailures := []error{
		fmt.Errorf("%w: x", errResolveFailed),
		errors.New("connection reset by peer"),
	}

	for _, err := range policyDenials {
		if status, _, _ := classify(err, "h"); status != http.StatusForbidden {
			t.Errorf("policy denial %v reported %d, want 403", err, status)
		}
	}
	for _, err := range upstreamFailures {
		if status, _, _ := classify(err, "h"); status == http.StatusForbidden {
			t.Errorf("upstream failure %v reported 403; it is not a policy decision", err)
		}
	}
}

// TestTheDialerTagsItsOwnRefusals checks the errors are produced where the decision is
// made. classify() can only tell these apart if dial() marks them, and a wrapped error
// that loses its sentinel silently degrades every message back to "upstream failed".
func TestTheDialerTagsItsOwnRefusals(t *testing.T) {
	p := New(discardLogger(), NewRegistry(discardLogger()), "127.0.0.1", 0, 0)

	_, err := p.dial("93.184.216.34", 443)
	if !errors.Is(err, errNotAHostname) {
		t.Errorf("dialing an IP literal gave %v, want errNotAHostname", err)
	}

	// A name that resolves only to loopback stands in for one resolving into
	// infrastructure: same code path, no external dependency.
	_, err = p.dial("localhost", 443)
	if !errors.Is(err, errAddressNotPermitted) {
		t.Errorf("dialing a non-public address gave %v, want errAddressNotPermitted", err)
	}

	_, err = p.dial("this-name-does-not-resolve.invalid", 443)
	if !errors.Is(err, errResolveFailed) {
		t.Errorf("an unresolvable name gave %v, want errResolveFailed", err)
	}
}
