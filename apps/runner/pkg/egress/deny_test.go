// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import "testing"

// TestDenialsBeatPermissions is the property an administrator has to be able to rely
// on. "The public internet, except this host" is only a usable sentence if the
// exception is final -- if the answer depended on which rule was checked first, the
// policy would be unpredictable exactly where it matters most.
func TestDenialsBeatPermissions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{"public internet permits an ordinary host", ParseAllowList("*,!ads.example.com"), "example.com", true},
		{"denial overrides the public internet", ParseAllowList("*,!ads.example.com"), "ads.example.com", false},
		{"denial overrides an explicit allow", ParseAllowList("example.com,!example.com"), "example.com", false},
		{"denial subtree", ParseAllowList("*,!*.tracker.test"), "a.tracker.test", false},
		{"denial subtree does not cover the apex", ParseAllowList("*,!*.tracker.test"), "tracker.test", true},
		{"unrelated host is unaffected", ParseAllowList("*,!*.tracker.test"), "example.com", true},
		{"denial alongside a named list", ParseAllowList("github.com,pypi.org,!pypi.org"), "github.com", true},
		{"a denial does not itself permit anything", ParseAllowList("!evil.test"), "example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allowed(tc.host, tc.patterns); got != tc.want {
				t.Errorf("Allowed(%q, %v) = %v, want %v", tc.host, tc.patterns, got, tc.want)
			}
		})
	}
}

// TestParseAllowListKeepsDenialsIntact guards the wire format. The marker has to
// survive normalization, or a denial would silently become a permission -- the one
// parsing bug in this file that fails open.
func TestParseAllowListKeepsDenialsIntact(t *testing.T) {
	got := ParseAllowList(" * , !Ads.Example.COM. , github.com ")
	want := []string{"*", "!ads.example.com", "github.com"}

	if len(got) != len(want) {
		t.Fatalf("ParseAllowList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseAllowList = %v, want %v", got, want)
		}
	}

	// The any-host posture is still recognised with denials present.
	if !AllowsAnyHost(got) {
		t.Error("denials suppressed the public-internet posture")
	}
}

// TestDenialsApplyToDNSToo: the resolver and the proxy share Allowed(), so a name an
// admin blocked does not resolve either. Asserted explicitly because the two paths
// having different answers is the failure mode that reads as "the network is flaky".
func TestDenialsApplyToDNSToo(t *testing.T) {
	patterns := ParseAllowList("*,!blocked.test")

	if Allowed("blocked.test", patterns) {
		t.Error("blocked name was permitted")
	}
	if !Allowed("allowed.test", patterns) {
		t.Error("unrelated name was refused")
	}
}
