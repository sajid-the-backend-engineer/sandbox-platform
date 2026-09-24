// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"encoding/json"
	"fmt"

	"github.com/docker/docker/profiles/seccomp"
	mobyseccomp "github.com/moby/profiles/seccomp"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// Namespace flags Chrome's layer-1 sandbox asks clone() and unshare() for.
//
// Only these three. The mask below is built from them, so a request carrying any
// OTHER namespace -- mount, UTS, IPC, cgroup -- still fails the filter.
const (
	cloneNewUser = 0x10000000
	cloneNewPID  = 0x20000000
	cloneNewNet  = 0x40000000

	// Every namespace bit Docker's own rule inspects. The filter compares against
	// this window, so bits outside it are irrelevant either way.
	cloneNamespaceMask = 0x7E020000
)

// browserSeccompProfile returns Docker's default profile plus the three allowances a
// browser needs to sandbox ITSELF, and nothing else.
//
// WHY THIS EXISTS AT ALL. A sandbox under a domain policy runs unprivileged with every
// capability dropped -- that is what makes the egress policy enforceable, because a
// privileged workload can rewrite its own source address and inherit a neighbour's
// allow list. Chrome's own sandbox needs to build namespaces, Docker's default profile
// refuses clone/unshare with namespace flags unless the container holds CAP_SYS_ADMIN,
// and restoring that capability would hand back exactly what was taken away.
//
// The alternative usually reached for is --no-sandbox, which turns Chrome's renderer
// isolation off entirely. This keeps it on: the container stays unprivileged and
// capless, and the filter is widened by three syscalls rather than one capability.
//
// EACH ALLOWANCE WAS TRACED, NOT ASSUMED. Running Chrome under a copy of this profile
// with defaultAction SCMP_ACT_LOG -- which records what it would have refused instead
// of refusing it -- named exactly one further syscall, 161 (chroot). The clone and
// unshare requirements came the same way, from strace: clone(CLONE_NEWUSER|SIGCHLD)
// was the first EPERM, unshare(CLONE_NEWUSER) the next. Nothing here is in the list
// because it seemed plausible.
//
// DELIBERATELY ABSENT:
//
//   - CLONE_NEWNS, CLONE_NEWUTS, CLONE_NEWIPC. Never appear in the trace. The mask
//     refuses them and Chrome does not care.
//   - clone3. Docker's default answers ENOSYS, glibc falls back to clone, and clone's
//     flags sit in a register seccomp can read. clone3 takes them in a struct it
//     cannot, so allowing it would be an unbounded hole wearing a narrow name.
//   - CAP_SYS_CHROOT. chroot still needs the capability; permitting the syscall does
//     not grant it. Chrome only holds it INSIDE the user namespace it just created,
//     which is the whole point -- the container itself still cannot chroot.
func browserSeccompProfile() (string, error) {
	// Built from the running Docker library rather than a vendored copy, so the base
	// can never drift from the daemon this runner talks to. A stale pinned profile is
	// the failure mode that turns "hardened" into "blocks syscalls the runtime needs".
	profile := seccomp.DefaultProfile()

	// MASKED_EQ compares (arg & value) == valueTwo. Value is every namespace bit EXCEPT
	// the three permitted, and valueTwo is zero -- so the call is allowed only when it
	// asks for none of the others.
	forbidden := uint64(^(cloneNewUser | cloneNewPID | cloneNewNet) & cloneNamespaceMask)
	namespaceArg := []specs.LinuxSeccompArg{{
		Index:    0,
		Value:    forbidden,
		ValueTwo: 0,
		Op:       specs.OpMaskedEqual,
	}}

	allowances := []*mobyseccomp.Syscall{
		{LinuxSyscall: specs.LinuxSyscall{
			Names: []string{"clone"}, Action: specs.ActAllow, Args: namespaceArg}},
		{LinuxSyscall: specs.LinuxSyscall{
			Names: []string{"unshare"}, Action: specs.ActAllow, Args: namespaceArg}},
		// No argument filter: chroot's only argument is a path, and the capability
		// check behind it is what actually constrains this.
		{LinuxSyscall: specs.LinuxSyscall{
			Names: []string{"chroot"}, Action: specs.ActAllow}},
	}

	// Prepended. The default profile already carries a narrower clone rule; a filter
	// permits a call if ANY rule matches, so ordering is not load-bearing, but keeping
	// the additions first makes them the first thing a reader sees.
	profile.Syscalls = append(allowances, profile.Syscalls...)

	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("encode browser seccomp profile: %w", err)
	}
	return string(encoded), nil
}
