// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import "testing"

// TestRestrictedEgressCoversEveryMode guards the classification that the privilege
// decision and the pre-flight baseline gate both key off.
//
// Miss a mode here and that mode silently keeps privileged mode -- and therefore
// CAP_NET_ADMIN, and therefore the ability to reassign the source address its own
// policy is selected by -- while also skipping the check that enforcement exists at
// all. The first version of this code classified only domainAllowList, which left
// block-all and CIDR sandboxes in exactly that state.
func TestRestrictedEgressCoversEveryMode(t *testing.T) {
	yes, no := true, false
	cidr := "10.0.0.0/8"
	domains := "pypi.org"
	empty := ""
	blank := "   "

	for _, tc := range []struct {
		name     string
		blockAll *bool
		cidr     *string
		domains  *string
		want     bool
	}{
		{"block all", &yes, nil, nil, true},
		{"cidr allow list", nil, &cidr, nil, true},
		{"domain allow list", nil, nil, &domains, true},
		{"block all false is not a restriction", &no, nil, nil, false},
		{"empty strings are not policies", nil, &empty, &empty, false},
		{"whitespace is not a policy", nil, &blank, &blank, false},
		{"nothing requested", nil, nil, nil, false},
		{"block all wins over an empty list", &yes, &empty, nil, true},
	} {
		if got := RestrictedEgress(tc.blockAll, tc.cidr, tc.domains); got != tc.want {
			t.Errorf("%s: RestrictedEgress = %v, want %v", tc.name, got, tc.want)
		}
	}
}
