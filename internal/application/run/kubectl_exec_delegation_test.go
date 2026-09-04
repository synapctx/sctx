package run

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/fs"
	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	kubectlfmt "github.com/synapctx/sctx/internal/adapters/format/kubectl"
	"github.com/synapctx/sctx/internal/adapters/format/read"
	"github.com/synapctx/sctx/internal/domain/format"
)

// lsFixture is `kubectl exec -n argocd argocd-redis-86846d5986-6lmgd --
// ls -la /etc` stdout captured against the real sct-euc3-fi1-prd-svc-01
// cluster (READ-ONLY: a directory listing).
const lsFixture = `total 168
drwxr-xr-x    1 root     root          4096 Aug 29 13:13 .
drwxr-xr-x    1 root     root          4096 Aug 29 13:13 ..
-rw-r--r--    1 root     root             7 Jan 27  2026 alpine-release
drwxr-xr-x    1 root     root          4096 Jan 28  2026 apk
drwxr-xr-x    2 root     root          4096 Jan 27  2026 busybox-paths.d
drwxr-xr-x    2 root     root          4096 Jan 27  2026 crontabs
-rw-r--r--    1 root     root            89 Mar 25  2025 fstab
-rw-r--r--    1 root     root           529 Jan 28  2026 group
-rw-r--r--    1 root     root           524 Jan 28  2026 group-
-rw-r--r--    1 root     root            30 Aug 29 13:13 hostname
-rw-r--r--    1 root     root           226 Aug 29 13:13 hosts
-rw-r--r--    1 root     root           570 Mar 25  2025 inittab
-rw-r--r--    1 root     root            51 Jan 27  2026 issue
drwxr-xr-x    2 root     root          4096 Jan 27  2026 logrotate.d
drwxr-xr-x    2 root     root          4096 Jan 27  2026 modprobe.d
-rw-r--r--    1 root     root            15 Mar 25  2025 modules
drwxr-xr-x    2 root     root          4096 Jan 27  2026 modules-load.d
-rw-r--r--    1 root     root           284 Mar 25  2025 motd
lrwxrwxrwx    1 root     root            14 Jan 27  2026 mtab -> ../proc/mounts
drwxr-xr-x    8 root     root          4096 Jan 27  2026 network
-rw-r--r--    1 root     root           205 Mar 25  2025 nsswitch.conf
drwxr-xr-x    2 root     root          4096 Jan 27  2026 opt
lrwxrwxrwx    1 root     root            21 Jan 27  2026 os-release -> ../usr/lib/os-release
-rw-r--r--    1 root     root           746 Jan 28  2026 passwd
-rw-r--r--    1 root     root           702 Mar 25  2025 passwd-
drwxr-xr-x    7 root     root          4096 Jan 27  2026 periodic
-rw-r--r--    1 root     root           547 Mar 25  2025 profile
drwxr-xr-x    2 root     root          4096 Jan 27  2026 profile.d
-rw-r--r--    1 root     root          3144 Mar 25  2025 protocols
-rw-r--r--    1 root     root           102 Aug 29 13:13 resolv.conf
drwxr-xr-x    2 root     root          4096 Jan 27  2026 secfixes.d
-rw-r--r--    1 root     root           156 Nov 21  2025 securetty
-rw-r--r--    1 root     root         12813 Mar 25  2025 services
-rw-r-----    1 root     shadow         287 Jan 28  2026 shadow
-rw-r-----    1 root     shadow         260 Jan 27  2026 shadow-
-rw-r--r--    1 root     root            38 Jan 28  2026 shells
drwxr-xr-x    4 root     root          4096 Jan 27  2026 ssl
drwxr-xr-x    2 root     root          4096 Jan 27  2026 ssl1.1
-rw-r--r--    1 root     root            53 Mar 25  2025 sysctl.conf
drwxr-xr-x    2 root     root          4096 Jan 27  2026 sysctl.d
drwxr-xr-x    2 root     root          4096 Jan 27  2026 udhcpc
`

// lsFixtureStderr is stderr from the same invocation: BusyBox's usual
// "Defaulted container" note, which kubectlTransportFailure must treat as
// noise rather than a transport failure, so the exec still delegates.
const lsFixtureStderr = "Defaulted container \"redis\" out of: redis, secret-init (init)\n"

// osReleaseFixture is `kubectl exec ... -- cat /etc/os-release` output from
// the same pod (READ-ONLY).
const osReleaseFixture = `NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.22.3
PRETTY_NAME="Alpine Linux v3.22"
HOME_URL="https://alpinelinux.org/"
BUG_REPORT_URL="https://gitlab.alpinelinux.org/alpine/aports/-/issues"
`

// goTestFixture is `go test ./... -v` output captured from this repository's
// own sed package (real binary, this platform).
const goTestFixture = `=== RUN   TestRecognizedShapesDelegateToRead
=== RUN   TestRecognizedShapesDelegateToRead/range_address
=== RUN   TestRecognizedShapesDelegateToRead/regex_address
--- PASS: TestRecognizedShapesDelegateToRead (0.00s)
    --- PASS: TestRecognizedShapesDelegateToRead/range_address (0.00s)
    --- PASS: TestRecognizedShapesDelegateToRead/regex_address (0.00s)
=== RUN   TestUnrecognizedShapesDecline
=== RUN   TestUnrecognizedShapesDecline/substitution
--- PASS: TestUnrecognizedShapesDecline (0.00s)
    --- PASS: TestUnrecognizedShapesDecline/substitution (0.00s)
=== RUN   TestNonZeroExitDeclinesToVerbatim
--- PASS: TestNonZeroExitDeclinesToVerbatim (0.00s)
PASS
ok  	github.com/synapctx/sctx/internal/adapters/format/sed	(cached)
`

// buildExecRegistry wires the same built-ins main.go registers that
// kubectl exec delegation actually needs, so ResolveBuiltInByArgv exercises
// the real path an agent's `kubectl exec ... -- ls|cat|go test` hits.
func buildExecRegistry() *Registry {
	r := NewRegistry()
	r.Register(gotest.New())
	for _, f := range fs.All() {
		r.Register(f)
	}
	for _, f := range read.All() {
		r.Register(f)
	}
	r.Register(kubectlfmt.New(r.ResolveBuiltInByArgv))
	return r
}

// TestKubectlExecDelegatesToLsOverRealFixture confirms the documented
// delegation path (kubectl exec formatter doc, CLAUDE.md nested-transport
// invariant) actually reaches the `ls` formatter for a common shape, using
// output captured from the real cluster rather than a synthetic stand-in.
func TestKubectlExecDelegatesToLsOverRealFixture(t *testing.T) {
	r := buildExecRegistry()
	argv := []string{"kubectl", "exec", "argocd-redis-86846d5986-6lmgd", "-n", "argocd", "--", "ls", "-la", "/etc"}
	fm, ok := r.ResolveBuiltInByArgv(argv)
	if !ok {
		t.Fatal("kubectl not registered")
	}
	in := format.Input{
		Argv:     argv,
		Stdout:   strings.NewReader(lsFixture),
		Stderr:   strings.NewReader(lsFixtureStderr),
		ExitCode: 0,
	}
	out, err := fm.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v, want delegation to fs's ls formatter", err)
	}
	if len(out.Body) == 0 || len(out.Body) >= len(lsFixture) {
		t.Fatalf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(lsFixture))
	}
	t.Logf("ls -la: %d -> %d bytes", len(lsFixture), len(out.Body))
}

// TestKubectlExecDelegatesToCatOverRealFixture confirms the same path for
// `cat`, using content that is genuinely NOT compressible (no JSON, no
// repeated lines) — read's own guarantee is to never truncate that, so a
// correct decline here is success, not failure, as long as the decline comes
// from read (i.e. delegation happened) rather than from kubectl's own exec
// argv parsing.
func TestKubectlExecDelegatesToCatOverRealFixture(t *testing.T) {
	r := buildExecRegistry()
	fm, ok := r.ResolveBuiltInByArgv([]string{"kubectl", "exec", "argocd-redis-86846d5986-6lmgd", "-n", "argocd", "--", "cat", "/etc/os-release"})
	if !ok {
		t.Fatal("kubectl not registered")
	}
	in := format.Input{
		Argv:     []string{"kubectl", "exec", "argocd-redis-86846d5986-6lmgd", "-n", "argocd", "--", "cat", "/etc/os-release"},
		Stdout:   strings.NewReader(osReleaseFixture),
		Stderr:   strings.NewReader(lsFixtureStderr),
		ExitCode: 0,
	}
	if _, err := fm.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable (delegated to cat, which correctly declines non-redundant prose)", err)
	}
	in2 := format.Input{
		Argv:     []string{"kubectl", "exec", "argocd-redis-86846d5986-6lmgd", "-n", "argocd", "--", "cat", "/etc/os-release"},
		Stdout:   strings.NewReader(osReleaseFixture),
		Stderr:   strings.NewReader(lsFixtureStderr),
		ExitCode: 0,
	}
	if _, err := fm.Relaxed(context.Background(), in2); err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

// TestKubectlExecDelegatesToGoTestOverRealFixture confirms the same path for
// `go test`, using real `go test -v` output captured from this repository.
func TestKubectlExecDelegatesToGoTestOverRealFixture(t *testing.T) {
	r := buildExecRegistry()
	argv := []string{"kubectl", "exec", "builder-pod", "--", "go", "test", "./...", "-v"}
	fm, ok := r.ResolveBuiltInByArgv(argv)
	if !ok {
		t.Fatal("kubectl not registered")
	}
	in := format.Input{
		Argv:     argv,
		Stdout:   strings.NewReader(goTestFixture),
		Stderr:   strings.NewReader(""),
		ExitCode: 0,
	}
	out, err := fm.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v, want delegation to gotest", err)
	}
	if len(out.Body) == 0 || len(out.Body) >= len(goTestFixture) {
		t.Fatalf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(goTestFixture))
	}
	t.Logf("go test -v: %d -> %d bytes", len(goTestFixture), len(out.Body))
}
