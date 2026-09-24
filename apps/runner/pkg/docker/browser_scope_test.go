// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/northrays/runner/cmd/runner/config"
	"github.com/northrays/runner/pkg/api/dto"
)

// TestMain loads the runner config once.
//
// getContainerHostConfig reads config.GetContainerRuntime(), which dereferences a
// package-level pointer that stays nil until GetConfig runs. In production that happens
// at startup, long before any container is created, so the accessor is right to assume
// it -- rather than weaken it for tests, the tests do what startup does. The two values
// below are the only ones GetConfig insists on.
func TestMain(m *testing.M) {
	_ = os.Setenv("NORTHRAYS_API_URL", "http://localhost:0")
	_ = os.Setenv("NORTHRAYS_RUNNER_TOKEN", "test-token")
	if _, err := config.GetConfig(); err != nil {
		panic("test setup: " + err.Error())
	}
	os.Exit(m.Run())
}

// These tests are about SCOPE, not about whether the profile is correct -- that is
// seccomp_test.go's job. The question here is which sandboxes receive it, because a
// widened syscall filter on a workload that has no browser is cost without benefit,
// and the same relaxation applied globally is what the design was chosen to avoid.

func testClient() *DockerClient {
	return &DockerClient{
		logger:                 slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		resourceLimitsDisabled: true,
	}
}

func hostConfigFor(t *testing.T, sandbox dto.CreateSandboxDTO) (seccompProfile string, privileged bool, capDrop []string) {
	t.Helper()
	cfg, err := d2HostConfig(t, sandbox)
	if err != nil {
		t.Fatalf("getContainerHostConfig: %v", err)
	}
	for _, opt := range cfg.SecurityOpt {
		if strings.HasPrefix(opt, "seccomp=") {
			seccompProfile = strings.TrimPrefix(opt, "seccomp=")
		}
	}
	return seccompProfile, cfg.Privileged, cfg.CapDrop
}

func d2HostConfig(t *testing.T, sandbox dto.CreateSandboxDTO) (cfg hostConfigView, err error) {
	t.Helper()
	hc, err := testClient().getContainerHostConfig(sandbox, nil, nil)
	if err != nil {
		return hostConfigView{}, err
	}
	return hostConfigView{
		SecurityOpt: hc.SecurityOpt,
		Privileged:  hc.Privileged,
		CapDrop:     stringsOf(hc.CapDrop),
	}, nil
}

type hostConfigView struct {
	SecurityOpt []string
	Privileged  bool
	CapDrop     []string
}

func stringsOf(caps []string) []string { return caps }

func restrictedSandbox(browser bool) dto.CreateSandboxDTO {
	domains := "example.com"
	b := browser
	s := dto.CreateSandboxDTO{
		Id:              "sandbox-under-test",
		DomainAllowList: &domains,
	}
	if browser {
		s.BrowserSandbox = &b
	}
	return s
}

func TestOrdinaryRestrictedSandboxDoesNotGetTheBrowserProfile(t *testing.T) {
	// The important half of the scope. Python and Node sandboxes are the overwhelming
	// majority, they never launch a browser, and widening their filter would add
	// kernel surface for nothing.
	profile, privileged, capDrop := hostConfigFor(t, restrictedSandbox(false))

	if profile != "" {
		t.Error("a restricted sandbox that did not declare a browser received a seccomp profile")
	}
	if privileged {
		t.Error("a restricted sandbox must not be privileged")
	}
	assertDropped(t, capDrop, "NET_ADMIN", "NET_RAW")
}

func TestBrowserRestrictedSandboxGetsTheProfile(t *testing.T) {
	profile, privileged, capDrop := hostConfigFor(t, restrictedSandbox(true))

	if profile == "" {
		t.Fatal("a browser sandbox did not receive the seccomp profile; Chrome cannot sandbox itself")
	}
	if !strings.Contains(profile, "chroot") || !strings.Contains(profile, "SCMP_CMP_MASKED_EQ") {
		t.Error("the applied profile is not the browser one")
	}
	// The whole point: the profile is added WITHOUT relaxing anything else.
	if privileged {
		t.Error("a browser sandbox must still be unprivileged")
	}
	assertDropped(t, capDrop, "NET_ADMIN", "NET_RAW")
}

func TestAnUnrestrictedBrowserSandboxIsUnchanged(t *testing.T) {
	// No egress policy means no dropped privilege to compensate for, so there is
	// nothing for the profile to restore. Applying it here would widen the filter on
	// a container that is already privileged -- strictly worse than leaving it alone.
	yes := true
	profile, _, _ := hostConfigFor(t, dto.CreateSandboxDTO{Id: "unrestricted", BrowserSandbox: &yes})

	if profile != "" {
		t.Error("an unrestricted browser sandbox received the profile; it is only for the restricted path")
	}
}

func TestTheProfileNeverRestoresCapSysAdmin(t *testing.T) {
	// CAP_SYS_ADMIN is the capability that would let a workload rewrite its own source
	// address and inherit a neighbour's egress allow list. The seccomp route exists
	// precisely so this never has to come back.
	// Asserted against the container's CAPABILITY configuration, not against the text
	// of the profile. Docker's default profile mentions CAP_SYS_ADMIN throughout, in
	// conditional rules of the form "allow X when the container HAS this capability" --
	// those grant nothing to a container that holds none, and searching the JSON for
	// the string would fail on every one of them while proving nothing.
	hc, err := testClient().getContainerHostConfig(restrictedSandbox(true), nil, nil)
	if err != nil {
		t.Fatalf("getContainerHostConfig: %v", err)
	}

	for _, c := range hc.CapAdd {
		if strings.EqualFold(c, "SYS_ADMIN") || strings.EqualFold(c, "CAP_SYS_ADMIN") {
			t.Error("CAP_SYS_ADMIN was added back; the seccomp route exists so it never has to be")
		}
	}
	if hc.Privileged {
		t.Error("privileged mode grants every capability including CAP_SYS_ADMIN")
	}
}

func TestBlockAllBrowserSandboxKeepsItsRestrictions(t *testing.T) {
	// block-all is restricted by a different route than a domain list, and the browser
	// flag must not quietly re-privilege it.
	yes := true
	block := true
	profile, privileged, capDrop := hostConfigFor(t, dto.CreateSandboxDTO{
		Id: "blocked-browser", NetworkBlockAll: &block, BrowserSandbox: &yes,
	})

	if profile == "" {
		t.Error("a block-all browser sandbox should still get the profile")
	}
	if privileged {
		t.Error("block-all must remain unprivileged")
	}
	assertDropped(t, capDrop, "NET_ADMIN", "NET_RAW")
}

func assertDropped(t *testing.T, capDrop []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, c := range capDrop {
			if strings.EqualFold(c, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not dropped; got CapDrop=%v", w, capDrop)
		}
	}
}
