// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker/profiles/seccomp"
)

// The three syscalls a browser needs to sandbox itself, and the namespaces it may ask
// for. Duplicated from seccomp.go on purpose: a test that imports the value it is
// checking cannot notice that value changing.
const (
	wantForbiddenMask = uint64(0x0E020000) // NEWNS | NEWUTS | NEWIPC -- must stay refused
	permittedMask     = uint64(0x70000000) // NEWUSER | NEWPID | NEWNET -- Chrome's three
)

func decodeProfile(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("profile is not valid JSON: %v", err)
	}
	return out
}

// rulesFor returns every rule naming the given syscall.
func rulesFor(t *testing.T, profile map[string]any, name string) []map[string]any {
	t.Helper()
	var found []map[string]any
	groups, _ := profile["syscalls"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		names, _ := group["names"].([]any)
		for _, n := range names {
			if s, _ := n.(string); s == name {
				found = append(found, group)
				break
			}
		}
	}
	return found
}

func TestBrowserProfileAllowsOnlyChromesThreeNamespaces(t *testing.T) {
	// The point of the mask. Chrome asks for user, PID and network namespaces; a
	// request carrying a mount, UTS or IPC namespace has to keep failing, or the
	// allowance stops being narrow and becomes "namespaces, generally".
	raw, err := browserSeccompProfile()
	if err != nil {
		t.Fatalf("browserSeccompProfile: %v", err)
	}
	profile := decodeProfile(t, raw)

	for _, call := range []string{"clone", "unshare"} {
		var allowRuleWithArgs map[string]any
		for _, rule := range rulesFor(t, profile, call) {
			if rule["action"] == "SCMP_ACT_ALLOW" && rule["args"] != nil {
				if args, _ := rule["args"].([]any); len(args) == 1 {
					arg, _ := args[0].(map[string]any)
					if v, ok := arg["value"].(float64); ok && uint64(v) == wantForbiddenMask {
						allowRuleWithArgs = rule
					}
				}
			}
		}
		if allowRuleWithArgs == nil {
			t.Fatalf("%s: no ALLOW rule masking off the forbidden namespaces (0x%X)", call, wantForbiddenMask)
		}
		args, _ := allowRuleWithArgs["args"].([]any)
		arg, _ := args[0].(map[string]any)
		if arg["op"] != "SCMP_CMP_MASKED_EQ" {
			t.Errorf("%s: expected SCMP_CMP_MASKED_EQ, got %v", call, arg["op"])
		}
		// valueTwo absent marshals as 0, which is the comparison we want:
		// (flags & forbidden) == 0.
		if v, present := arg["valueTwo"]; present && v.(float64) != 0 {
			t.Errorf("%s: valueTwo must be 0, got %v", call, v)
		}
	}

	if wantForbiddenMask&permittedMask != 0 {
		t.Fatal("the masks overlap; the test constants are wrong")
	}
}

func TestBrowserProfileAllowsChroot(t *testing.T) {
	// Found by running Chrome under SCMP_ACT_LOG, which named exactly one further
	// syscall: 161. Chrome's layer-1 sandbox chroots the renderer into an empty
	// directory.
	raw, err := browserSeccompProfile()
	if err != nil {
		t.Fatalf("browserSeccompProfile: %v", err)
	}
	for _, rule := range rulesFor(t, decodeProfile(t, raw), "chroot") {
		if rule["action"] == "SCMP_ACT_ALLOW" && rule["includes"] == nil {
			return // allowed unconditionally by our rule
		}
	}
	t.Error("chroot is not allowed; Chrome's sandbox cannot pivot the renderer")
}

func TestBrowserProfileLeavesClone3Blocked(t *testing.T) {
	// clone3 takes its flags in a struct, which seccomp cannot read. Docker answers
	// ENOSYS so glibc falls back to clone, whose flags sit in a register the filter
	// CAN inspect. Allowing clone3 would be an unbounded hole wearing a narrow name.
	raw, err := browserSeccompProfile()
	if err != nil {
		t.Fatalf("browserSeccompProfile: %v", err)
	}
	// Docker's default carries TWO clone3 rules: an ALLOW guarded by
	// includes.caps=[CAP_SYS_ADMIN], and the ENOSYS that applies to everyone else. The
	// guarded one can never fire here -- this container has no capabilities at all --
	// so the assertion is about UNGUARDED allows, which would fire.
	sawUnguardedAllow := false
	sawErrno := false
	for _, rule := range rulesFor(t, decodeProfile(t, raw), "clone3") {
		switch rule["action"] {
		case "SCMP_ACT_ALLOW":
			if inc, present := rule["includes"]; !present || inc == nil ||
				!strings.Contains(toJSON(t, inc), "CAP_SYS_ADMIN") {
				sawUnguardedAllow = true
			}
		case "SCMP_ACT_ERRNO":
			sawErrno = true
		}
	}
	if sawUnguardedAllow {
		t.Error("clone3 is allowed without a capability guard; its flags are unreadable " +
			"to seccomp, so it must stay ENOSYS and let glibc fall back to clone")
	}
	if !sawErrno {
		t.Error("clone3 has no ENOSYS rule; glibc would not fall back to the filterable clone")
	}
}

func TestBrowserProfileKeepsTheDockerDefaultUnderneath(t *testing.T) {
	// The allowances are added to Docker's profile, not substituted for it. If the
	// base were dropped, everything it refuses -- keyctl, ptrace of other containers,
	// the rest -- would quietly become permitted.
	raw, err := browserSeccompProfile()
	if err != nil {
		t.Fatalf("browserSeccompProfile: %v", err)
	}
	profile := decodeProfile(t, raw)

	if profile["defaultAction"] != "SCMP_ACT_ERRNO" {
		t.Errorf("defaultAction is %v, want SCMP_ACT_ERRNO -- the profile must deny by default",
			profile["defaultAction"])
	}

	base := seccomp.DefaultProfile()
	got, _ := profile["syscalls"].([]any)
	if len(got) != len(base.Syscalls)+3 {
		t.Errorf("expected Docker's %d groups plus exactly 3, got %d",
			len(base.Syscalls), len(got))
	}
}

func TestBrowserProfileNeverGrantsCapSysAdmin(t *testing.T) {
	// The capability this whole design exists to avoid restoring. CAP_SYS_ADMIN is
	// what would let a workload rewrite its own source address and inherit a
	// neighbour's egress allow list.
	raw, err := browserSeccompProfile()
	if err != nil {
		t.Fatalf("browserSeccompProfile: %v", err)
	}
	// It may legitimately appear inside Docker's own conditional rules ("allow X when
	// the container HAS this capability"), which grant nothing on their own. What must
	// not appear is our additions depending on it.
	profile := decodeProfile(t, raw)
	groups, _ := profile["syscalls"].([]any)
	for i, g := range groups[:3] { // our three, prepended
		group, _ := g.(map[string]any)
		if inc, present := group["includes"]; present && inc != nil {
			if strings.Contains(strings.ToUpper(toJSON(t, inc)), "CAP_SYS_ADMIN") {
				t.Errorf("added rule %d is conditioned on CAP_SYS_ADMIN", i)
			}
		}
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
