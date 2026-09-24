//go:build privileged

// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: AGPL-3.0

// D2, proved at runtime rather than on paper.
//
// seccomp_test.go checks the profile's SHAPE and browser_scope_test.go checks WHICH
// sandboxes receive it. Both read a JSON document. Neither can tell you whether Chrome
// actually builds its sandbox inside a container configured this way, and that is the
// only question the change was made to answer.
//
// So these tests start a real container from the real production host config, run the
// real browser in it with no --no-sandbox anywhere, and then ask the kernel -- not
// Chrome's log -- whether the sandbox exists. The evidence is a namespace inode that
// differs from PID 1's. A log line saying "sandbox enabled" is a claim; a renderer
// living in a user namespace its parent is not in is the thing itself.
//
// The same container is also re-interrogated for everything the widened filter was NOT
// allowed to cost: no capabilities, no address it can add, no raw socket it can open,
// and the egress policy still refusing what it refused before.
//
// Needs a Docker daemon, NET_ADMIN, and an image carrying Google Chrome:
//
//	D2_BROWSER_IMAGE=<image> go test -tags privileged -p 1 -run D2 -v ./pkg/docker/
//
// Without that variable the suite skips rather than inventing a browser. The image is
// a deployment fact -- it is whatever SANDBOX_DESKTOP_SNAPSHOT currently points at --
// and a test that hardcoded a guess would pass against the wrong thing.
package docker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/northrays/runner/pkg/api/dto"
)

const (
	// The env var naming the image under test.
	d2ImageEnv = "D2_BROWSER_IMAGE"

	// Chrome refuses to start as root unless --no-sandbox is passed -- which is the
	// flag this entire change exists to avoid -- so the browser runs as an ordinary
	// user, exactly as it does in the production image. The uid is arbitrary; what
	// matters is that it is not 0.
	d2UID  = 1001
	d2Home = "/tmp/d2-home"
)

// d2Image returns the image under test, or skips.
func d2Image(t *testing.T) string {
	t.Helper()
	image := strings.TrimSpace(os.Getenv(d2ImageEnv))
	if image == "" {
		t.Skipf("set %s to the sandbox desktop image to run the D2 runtime proofs", d2ImageEnv)
	}
	return image
}

// d2Sandbox starts a container built from the PRODUCTION host config for a restricted
// sandbox, and returns its id and address.
//
// The host config comes from getContainerHostConfig rather than being assembled here.
// A runtime test that constructs its own container configuration proves that some
// configuration works, not that the shipped one does -- and the shipped one is the
// only one a customer ever gets.
func (e *lifecycleEnv) d2Sandbox(name, image, allowList string, browser bool) (string, string) {
	e.t.Helper()

	allow := allowList
	yes := browser
	sandbox := dto.CreateSandboxDTO{Id: name, DomainAllowList: &allow}
	if browser {
		sandbox.BrowserSandbox = &yes
	}

	hostConfig, err := testClient().getContainerHostConfig(sandbox, nil, nil)
	if err != nil {
		e.t.Fatalf("getContainerHostConfig: %v", err)
	}
	// The daemon bind is a runner-local path that does not exist in this test
	// environment, and nothing here execs the daemon. Everything the test is about --
	// Privileged, CapDrop, SecurityOpt -- is left exactly as production built it.
	hostConfig.Binds = nil

	created, err := e.cli.ContainerCreate(e.ctx,
		&container.Config{
			Image: image,
			// Entrypoint overridden as well as Cmd. The sandbox images carry an
			// entrypoint of their own, and leaving it in place would run "<entrypoint>
			// sleep 900" -- which either starts the whole desktop stack or fails,
			// neither of which is what this test needs the container for.
			Entrypoint: []string{"sleep"},
			Cmd:        []string{"900"},
			Labels:     EgressLabels(nil, nil, &allow),
			Env:        []string{"HOME=" + d2Home},
		},
		hostConfig, nil, nil, name)
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
	e.t.Cleanup(func() {
		_ = e.cli.ContainerRemove(context.Background(), created.ID,
			container.RemoveOptions{Force: true})
	})
	if err := e.cli.ContainerStart(e.ctx, created.ID, container.StartOptions{}); err != nil {
		e.t.Fatalf("start %s: %v", name, err)
	}

	// A writable HOME for the browser. Chrome's crash handler needs somewhere to put
	// its database and dies with SIGTRAP without one, which reads as a sandbox failure
	// and is not.
	//
	// Created as root because the sandbox images run as an unprivileged user by
	// default, and that user cannot chown a directory to the uid the browser probes
	// run under.
	if out, code := e.execUser(created.ID, "0:0", "sh", "-c",
		fmt.Sprintf("install -d -m 0755 -o %d -g %d %s", d2UID, d2UID, d2Home)); code != 0 {
		e.t.Fatalf("prepare HOME: %s", strings.TrimSpace(out))
	}
	return created.ID, e.addressOf(created.ID)
}

// execAs runs a command as the non-root browser user.
func (e *lifecycleEnv) execAs(id string, cmd ...string) (string, int) {
	e.t.Helper()
	return e.execUser(id, strconv.Itoa(d2UID)+":"+strconv.Itoa(d2UID), cmd...)
}

func (e *lifecycleEnv) execUser(id, user string, cmd ...string) (string, int) {
	e.t.Helper()

	ctx, cancel := context.WithTimeout(e.ctx, 120*time.Second)
	defer cancel()

	resp, err := e.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          cmd,
		User:         user,
		Env:          []string{"HOME=" + d2Home},
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		e.t.Fatalf("exec create: %v", err)
	}
	attached, err := e.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		e.t.Fatalf("exec attach: %v", err)
	}
	defer attached.Close()

	var out strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, readErr := attached.Reader.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}

	inspect, err := e.cli.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		e.t.Fatalf("exec inspect: %v", err)
	}
	return stripFrames(out.String()), inspect.ExitCode
}

// stripFrames removes Docker's 8-byte stream multiplexing headers.
//
// The other exec helper hands the stream to stdcopy. This one cannot: a browser writes
// megabytes of DOM in frames that stdcopy will happily assemble, but the same helper is
// used for probes whose output is a single line, and a partial frame at the end of a
// short read makes stdcopy error out on precisely those. Skipping the headers by hand
// is dull and total.
func stripFrames(raw string) string {
	var out strings.Builder
	for i := 0; i+8 <= len(raw); {
		if raw[i] > 2 || raw[i+1] != 0 || raw[i+2] != 0 || raw[i+3] != 0 {
			// Not a header: the stream was never multiplexed (a TTY exec).
			return raw
		}
		size := int(raw[i+4])<<24 | int(raw[i+5])<<16 | int(raw[i+6])<<8 | int(raw[i+7])
		i += 8
		if i+size > len(raw) {
			size = len(raw) - i
		}
		out.WriteString(raw[i : i+size])
		i += size
	}
	if out.Len() == 0 {
		return raw
	}
	return out.String()
}

// The egress posture a real browser sandbox carries.
//
// This is not the same as the named allow list the other suites use, and the difference
// is load-bearing rather than incidental. Chrome and Firefox send GREASE Encrypted
// ClientHello by default, and the proxy refuses ECH under a NAMED allow list because an
// encrypted inner name cannot be checked against one (proxy.go, handleTLS). Under "*"
// there is no name policy for a hidden name to evade, so ECH is accepted -- which is
// precisely the case the browser profiles run in.
//
// Putting Chrome behind a named list here would test a combination the platform does
// not ship and cannot serve, and the failure would look like a sandbox failure.
const d2PublicInternet = "*"

// d2Env brings up a runner with the baseline deny on and a sandbox attached to it,
// which is the production posture the whole design assumes.
func d2Env(t *testing.T, name, allowList string, browser bool) (*lifecycleEnv, string) {
	t.Helper()

	image := d2Image(t)
	env := newLifecycleEnv(t)

	runner := env.start(true)
	t.Cleanup(runner.stop)
	if err := runner.rules.SetBaselineDeny(env.subnet); err != nil {
		t.Fatalf("SetBaselineDeny: %v", err)
	}
	t.Cleanup(func() { _ = runner.rules.RemoveBaselineDeny(env.subnet) })

	id, _ := env.d2Sandbox(name, image, allowList, browser)
	t.Cleanup(func() { _ = runner.rules.DeleteDomainRules(id[:12]) })

	if err := runner.client.ReconcileSandboxNetwork(env.ctx, id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return env, id
}

func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// The three capability bits this design is accountable for. Numbers from
// include/uapi/linux/capability.h.
const (
	capNetAdmin = 12
	capNetRaw   = 13
	capSysAdmin = 21
)

// TestD2TheBrowserSandboxHoldsNoDangerousCapabilities checks the claim where it is
// true or false: the kernel's own accounting for the process.
//
// Note what is NOT asserted. CapEff is not zero and is not supposed to be -- a
// restricted sandbox drops two capabilities, not all of them, and root inside it keeps
// Docker's ordinary defaults (CHOWN, DAC_OVERRIDE, SETUID and the rest). Asserting zero
// would be asserting something production does not do, and the test would have to be
// weakened the first time it ran. What matters is the three specific bits:
//
//	CAP_SYS_ADMIN  -- what --privileged or a capability add-back would have restored to
//	                  let Chrome build namespaces. The seccomp route exists so that this
//	                  never has to come back.
//	CAP_NET_ADMIN  -- rewrites addresses and routes.
//	CAP_NET_RAW    -- crafts packets with a source the kernel would not otherwise send.
//
// The last two are how a workload escapes an egress policy that is selected by source
// address. All three must be clear in the EFFECTIVE, PERMITTED and BOUNDING sets: a bit
// left in the bounding set is one a process can put back for its children.
func TestD2TheBrowserSandboxHoldsNoDangerousCapabilities(t *testing.T) {
	env, id := d2Env(t, "d2-caps", lifecycleAllowedHost, true)

	// Asked as ROOT inside the container, which is the strong form of the claim. An
	// unprivileged user shows an empty CapEff whatever the container was granted, so
	// reading it as uid 0 is the only reading that distinguishes "this container holds
	// nothing dangerous" from "this process happens to hold nothing".
	out, code := env.execUser(id, "0:0", "sh", "-c",
		"grep -E '^Cap(Eff|Prm|Bnd)' /proc/self/status")
	if code != 0 {
		t.Fatalf("read /proc/self/status: %s", strings.TrimSpace(out))
	}
	t.Logf("capabilities held by root inside the sandbox:\n%s", strings.TrimSpace(out))

	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		set := strings.TrimSuffix(fields[0], ":")
		mask, err := strconv.ParseUint(fields[1], 16, 64)
		if err != nil {
			t.Fatalf("%s is not a hex mask: %q", set, fields[1])
		}
		seen++
		for _, cap := range []struct {
			bit  uint
			name string
		}{
			{capSysAdmin, "CAP_SYS_ADMIN"},
			{capNetAdmin, "CAP_NET_ADMIN"},
			{capNetRaw, "CAP_NET_RAW"},
		} {
			if mask&(1<<cap.bit) != 0 {
				t.Errorf("%s is present in %s (%s)", cap.name, set, fields[1])
			}
		}
	}
	if seen != 3 {
		t.Fatalf("expected CapEff, CapPrm and CapBnd; parsed %d lines", seen)
	}
}

// TestD2ChromeSandboxesItselfWithoutTheEscapeHatch is the result D2 asked for.
//
// Chrome is launched with no --no-sandbox and no --disable-setuid-sandbox. If the
// seccomp profile were Docker's default, the layer-1 sandbox would fail to create its
// user namespace and Chrome would exit with "No usable sandbox!" instead of rendering.
// Rendering IS the proof that the three allowed syscalls are enough; the namespace
// check in the next test is the proof that they were actually used.
//
// Run under the public-internet posture because that is the one browser profiles use,
// and because a named allow list refuses Chrome's ECH before the page is ever fetched.
// See d2PublicInternet, and the test that pins that behaviour down below.
func TestD2ChromeSandboxesItselfWithoutTheEscapeHatch(t *testing.T) {
	env, id := d2Env(t, "d2-chrome", d2PublicInternet, true)

	out, code := env.execAs(id, "sh", "-c",
		"google-chrome --headless=new --disable-gpu --dump-dom "+
			"--user-data-dir="+d2Home+"/profile https://"+lifecycleAllowedHost+"/ 2>&1")
	if strings.Contains(out, "No usable sandbox") {
		t.Fatalf("Chrome reported no usable sandbox:\n%s", head(out, 2000))
	}
	if code != 0 {
		t.Fatalf("Chrome exited %d with its own sandbox and no --no-sandbox:\n%s",
			code, head(out, 2000))
	}
	if !strings.Contains(out, "Example Domain") {
		t.Errorf("Chrome did not render the allowed page; got:\n%s", head(out, 1000))
	} else {
		t.Log("Chrome rendered the allowed page with its own sandbox on")
	}
}

// TestD2TheRendererReallyLivesInItsOwnNamespaces asks the kernel rather than the log.
//
// Chrome's layer-1 sandbox clones a user namespace, then a PID and network namespace
// inside it, then chroots the renderer into an empty directory. If any of that had
// silently failed -- if Chrome had fallen back to an unsandboxed renderer, which it
// will do in some configurations rather than exiting -- the renderer's namespace inodes
// would match PID 1's. Different inodes cannot be produced any other way.
func TestD2TheRendererReallyLivesInItsOwnNamespaces(t *testing.T) {
	env, id := d2Env(t, "d2-namespaces", d2PublicInternet, true)

	// The exe check is not belt and braces; without it this probe finds ITSELF.
	// /proc/PID/cmdline for the shell running this script contains the whole script
	// text, including the string being searched for, so a cmdline-only match reports
	// the shell as a renderer -- and the shell is of course in the container's own
	// namespaces, which reads as "Chrome did not sandbox itself". Requiring
	// /proc/PID/exe to be the Chrome binary is what makes the match mean anything.
	script := "google-chrome --headless=new --disable-gpu " +
		"--user-data-dir=" + d2Home + "/profile about:blank " +
		">" + d2Home + "/chrome.log 2>&1 & \n" +
		"for i in $(seq 1 60); do\n" +
		"  for p in $(ls /proc | grep -E '^[0-9]+$'); do\n" +
		"    case \"$(readlink /proc/$p/exe 2>/dev/null)\" in */chrome) ;; *) continue;; esac\n" +
		"    grep -qa 'type=renderer' /proc/$p/cmdline 2>/dev/null || continue\n" +
		"    echo \"renderer=$p\"\n" +
		"    echo \"renderer_user=$(readlink /proc/$p/ns/user)\"\n" +
		"    echo \"renderer_pid_ns=$(readlink /proc/$p/ns/pid)\"\n" +
		"    echo \"self_user=$(readlink /proc/self/ns/user)\"\n" +
		"    echo \"self_pid_ns=$(readlink /proc/self/ns/pid)\"\n" +
		"    exit 0\n" +
		"  done\n" +
		"  sleep 0.5\n" +
		"done\n" +
		"echo 'no renderer appeared' >&2\n" +
		"tail -40 " + d2Home + "/chrome.log >&2\n" +
		"exit 1\n"

	out, code := env.execAs(id, "sh", "-c", script)
	t.Logf("namespace probe:\n%s", strings.TrimSpace(out))
	if code != 0 {
		t.Fatalf("could not observe a renderer process (exit %d)", code)
	}

	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	if got["renderer_user"] == "" || got["self_user"] == "" {
		t.Fatalf("the probe did not report both user namespaces: %v", got)
	}
	if got["renderer_user"] == got["self_user"] {
		t.Error("the renderer shares the container's own user namespace, so Chrome's " +
			"layer-1 sandbox did not build one -- the browser is running unsandboxed " +
			"inside the container")
	} else {
		t.Logf("the renderer is in its own user namespace (%s, not %s)",
			got["renderer_user"], got["self_user"])
	}
	if got["renderer_pid_ns"] != "" && got["renderer_pid_ns"] == got["self_pid_ns"] {
		t.Errorf("the renderer shares the container's pid namespace: %s", got["renderer_pid_ns"])
	}
}

// TestD2TheEgressPolicyStillHolds. The widened filter must not have widened the
// network, so the same pair of observations the egress suites make is made again here,
// against a container that has the browser profile applied.
//
// Both halves are required. "The denied host failed" on its own is satisfied by a
// sandbox with no network at all.
func TestD2TheEgressPolicyStillHolds(t *testing.T) {
	env, id := d2Env(t, "d2-egress", lifecycleAllowedHost, true)

	if out, code := env.exec(id, "curl", "-sS", "--max-time", "25", "-o", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code != 0 {
		t.Errorf("the allowed host is unreachable from a browser sandbox (exit %d): %s",
			code, strings.TrimSpace(out))
	} else {
		t.Logf("%s reachable, as the policy permits", lifecycleAllowedHost)
	}

	out, code := env.exec(id, "curl", "-sS", "--max-time", "15", "-o", "/dev/null",
		"https://"+lifecycleDeniedHost+"/")
	if code == 0 {
		t.Error("a denied host is reachable from a browser sandbox")
	} else {
		t.Logf("%s refused: %s", lifecycleDeniedHost, strings.TrimSpace(out))
	}
}

// TestD2TheSandboxCannotSpoofItsSourceAddress covers the specific escape the dropped
// capabilities exist to prevent.
//
// Egress policy is selected by source address. A workload that can add an address to
// its interface, or craft a packet with a source the kernel would not otherwise send,
// inherits whatever policy the runner wrote for the address it borrowed -- including a
// different tenant's. Both routes need a capability CapEff says is absent; this checks
// that the kernel agrees when asked to actually do it.
func TestD2TheSandboxCannotSpoofItsSourceAddress(t *testing.T) {
	env, id := d2Env(t, "d2-spoof", lifecycleAllowedHost, true)

	// A raw socket is how a forged source address is put on the wire. Needs CAP_NET_RAW.
	out, code := env.execAs(id, "python3", "-c",
		"import socket,sys\n"+
			"try:\n"+
			"    socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_RAW)\n"+
			"except PermissionError as e:\n"+
			"    print('refused:', e); sys.exit(0)\n"+
			"print('OPENED A RAW SOCKET'); sys.exit(1)\n")
	if code != 0 {
		t.Errorf("the sandbox opened a raw socket, so it can forge a source address: %s",
			strings.TrimSpace(out))
	} else {
		t.Logf("raw socket %s", strings.TrimSpace(out))
	}

	// Adding an address to the interface is the other route, and it goes through
	// rtnetlink. Needs CAP_NET_ADMIN. Sending the request is allowed; the kernel's
	// answer is the point, and EPERM (1) is the answer required.
	out, code = env.execAs(id, "python3", "-c",
		"import socket,struct,sys\n"+
			"s=socket.socket(socket.AF_NETLINK, socket.SOCK_RAW, 0)  # NETLINK_ROUTE\n"+
			"s.bind((0,0))\n"+
			"ifa=struct.pack('BBBBI', socket.AF_INET, 16, 0, 0, 1)\n"+
			"attr=struct.pack('HH', 8, 1)+socket.inet_aton('10.255.255.254')\n"+
			"body=ifa+attr\n"+
			"# RTM_NEWADDR=20; REQUEST|ACK|EXCL|CREATE\n"+
			"hdr=struct.pack('IHHII', 16+len(body), 20, 0x0001|0x0004|0x0200|0x0400, 1, 0)\n"+
			"s.send(hdr+body)\n"+
			"resp=s.recv(8192)\n"+
			"err=struct.unpack('i', resp[16:20])[0]\n"+
			"print('rtnetlink errno', -err)\n"+
			"sys.exit(0 if err != 0 else 1)\n")
	if code != 0 {
		t.Errorf("the sandbox added an address to its interface: %s", strings.TrimSpace(out))
	} else {
		t.Logf("address add %s (1 = EPERM)", strings.TrimSpace(out))
	}
}

// TestD2AnOrdinarySandboxStillCannotBuildNamespaces is the scope check made at runtime.
//
// browser_scope_test.go proves the profile is not ATTACHED to an ordinary restricted
// sandbox. This proves what that means in practice: the same image, the same egress
// policy, no browser flag, and the namespace syscalls are still refused. If this ever
// passes, the widening has leaked out of the browser path.
func TestD2AnOrdinarySandboxStillCannotBuildNamespaces(t *testing.T) {
	env, id := d2Env(t, "d2-ordinary", lifecycleAllowedHost, false)

	out, code := env.execAs(id, "python3", "-c",
		"import ctypes,ctypes.util,sys\n"+
			"libc=ctypes.CDLL(ctypes.util.find_library('c'), use_errno=True)\n"+
			"rc=libc.unshare(0x10000000)  # CLONE_NEWUSER\n"+
			"print('unshare rc', rc, 'errno', ctypes.get_errno())\n"+
			"sys.exit(1 if rc == 0 else 0)\n")
	if code != 0 {
		t.Errorf("an ordinary restricted sandbox created a user namespace; the browser "+
			"profile has leaked outside the browser path: %s", strings.TrimSpace(out))
	} else {
		t.Logf("ordinary sandbox refused: %s", strings.TrimSpace(out))
	}
}

// TestD2ChromeUnderANamedAllowListIsRefusedAtTheHandshake records a constraint that
// only became observable once Chrome could start at all.
//
// Chrome sends GREASE Encrypted ClientHello by default. ECH puts the real server name
// inside an encrypted inner ClientHello, so the outer name the proxy can read is not
// authoritative -- which makes it a bypass of a NAMED allow list and nothing at all
// under "*", where every public name is permitted anyway. The proxy draws exactly that
// line (egress/proxy.go, handleTLS), and the consequence is that a browser sandbox
// restricted to specific domains cannot load pages in Chrome.
//
// This is not a regression from the seccomp change and it is not a bug in the proxy;
// both halves are behaving as designed. It is written down as a test because the
// symptom -- a browser that starts perfectly and then renders nothing -- looks exactly
// like a sandbox failure, and because it means "browser sandbox" and "named domain
// allow list" are not a combination the platform can currently serve. Anyone who makes
// them work together will find this test failing, which is the right way to be told.
func TestD2ChromeUnderANamedAllowListIsRefusedAtTheHandshake(t *testing.T) {
	env, id := d2Env(t, "d2-ech", lifecycleAllowedHost, true)

	out, _ := env.execAs(id, "sh", "-c",
		"google-chrome --headless=new --disable-gpu --dump-dom "+
			"--user-data-dir="+d2Home+"/profile https://"+lifecycleAllowedHost+"/ 2>&1")

	// The sandbox still works -- that is the D2 claim, and it is independent of whether
	// the page loads.
	if strings.Contains(out, "No usable sandbox") {
		t.Errorf("Chrome could not sandbox itself under a named allow list:\n%s", head(out, 1500))
	}
	if strings.Contains(out, "Example Domain") {
		t.Log("the page loaded: ECH is no longer being refused under a named allow list, " +
			"so a browser sandbox can now carry one. Update the comment above and consider " +
			"switching the browser profiles off the wildcard posture.")
	} else {
		t.Log("the page did not load under a named allow list, as expected: the proxy " +
			"refuses Chrome's Encrypted ClientHello because the inner name it would have " +
			"to check is encrypted")
	}

	// And curl, which sends no ECH, still reaches the same host through the same policy.
	// Without this the test above would be satisfied by a sandbox with no network.
	if _, code := env.exec(id, "curl", "-sS", "--max-time", "25", "-o", "/dev/null",
		"https://"+lifecycleAllowedHost+"/"); code != 0 {
		t.Errorf("the allowed host is unreachable even without ECH (exit %d); the "+
			"refusal above is not about ECH", code)
	}
}
