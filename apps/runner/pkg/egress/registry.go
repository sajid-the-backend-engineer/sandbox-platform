// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package egress

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Registry holds the allow list for each sandbox, keyed by the address the runner
// assigned it.
//
// The proxy and the resolver share one of these deliberately. Two stores would be
// two things to keep in step, and the failure mode of them disagreeing is the worst
// one available: a name the resolver answers but the proxy refuses looks like a
// network fault, and a name the proxy allows but the resolver refuses is an outage
// nobody can explain from either component alone.
//
// The address is the identity. Packets reach the proxy and the resolver only because
// iptables redirected them out of a particular sandbox's veth, so the source address
// is one the runner assigned and not one the workload chose. That holds only as long
// as the sandbox cannot spoof it -- see the anti-spoof rule in netrules.
type Registry struct {
	log      *slog.Logger
	mu       sync.RWMutex
	policies map[string]Policy
}

// Policy is one sandbox's effective egress rules.
type Policy struct {
	// Patterns is the normalized allow list. Empty means deny everything, which is
	// not the same as absent: an allow list that parses to nothing is still an
	// allow list, and permits nothing.
	Patterns []string

	// Revision identifies the version of the policy in force, so a decision in the
	// log can be tied to the rules that produced it and a stale update can be
	// recognised as stale.
	Revision string
}

func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		log:      logger.With(slog.String("component", "egress_registry")),
		policies: make(map[string]Policy),
	}
}

// Register installs or replaces a sandbox's policy.
func (r *Registry) Register(sandboxIP string, policy Policy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[sandboxIP] = policy
	r.log.Info("Egress policy registered",
		"sandboxIp", sandboxIP, "revision", policy.Revision, "allowed", policy.Patterns)
}

// Unregister drops a sandbox's policy.
//
// This is what stops an address handed to a new sandbox from inheriting the previous
// tenant's authorization, so it runs on teardown before the IP can be reused.
func (r *Registry) Unregister(sandboxIP string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.policies[sandboxIP]; ok {
		delete(r.policies, sandboxIP)
		r.log.Info("Egress policy removed", "sandboxIp", sandboxIP)
	}
}

// For returns the policy for an address. The second result distinguishes "this
// sandbox may reach nothing" from "I have never heard of this sandbox" -- both are
// refusals, but only the second one means the firewall and the registry have drifted
// apart, which is worth logging differently.
func (r *Registry) For(sandboxIP string) (Policy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	policy, ok := r.policies[sandboxIP]
	return policy, ok
}

// Count reports how many sandboxes currently have a policy, for readiness reporting.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.policies)
}

// Revision is a short, stable identifier for an allow list.
//
// It exists so a decision in the log can be tied to the exact rules that produced
// it, and so an update can be recognised as having actually changed something. The
// patterns are sorted first: the same policy written in a different order is the
// same policy, and a revision that changed under reordering would report drift that
// is not there.
func Revision(patterns []string) string {
	sorted := append([]string(nil), patterns...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(sum[:6])
}
