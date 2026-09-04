// Package binaries answers "which copies of a program exist on this
// machine, and what version does each report" — the question behind `sctx
// doctor` warning about a SHADOWED or stale sctx: a customer with a dev
// build ahead of a Homebrew one on PATH sees the dev build run while an
// installed hook still calls the older one, and nothing about that is
// visible from inside either binary alone.
package binaries

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Found is one copy of a program located on PATH.
type Found struct {
	// Path is the resolved, symlink-free absolute path — the identity used
	// for deduplication, since a PATH entry and a symlink alias for the same
	// binary must count once, not twice.
	Path string
	// Version is whatever `<path> version` printed to stdout, trimmed; empty
	// when the binary could not be run or timed out.
	Version string
}

// OnPath returns every distinct copy of exeName found by searching pathEnv
// (a colon/semicolon-separated PATH string, in the OS's own list-separator
// convention) left to right, in the order PATH would resolve them.
//
// Distinct means distinct after resolving symlinks: a shim, a symlinked
// shell wrapper, and the real binary they all point at must count once, so a
// PATH with three aliases for one install does not report three
// installations. A path that fails to resolve (dangling symlink, permission
// error) is kept as-is rather than dropped — a broken PATH entry is still
// something worth reporting, not something to hide by silently skipping it.
func OnPath(pathEnv, exeName string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, exeName)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			resolved = candidate
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// versionTimeout bounds how long VersionOf waits for `<path> version`. Long
// enough for a cold-started binary, short enough that `sctx doctor` never
// hangs because one PATH entry is a hung or interactive program that also
// happens to be named the same as what we are looking for.
const versionTimeout = 2 * time.Second

// VersionOf runs `<path> version` and returns its trimmed stdout, or "" if
// the binary could not be run, exited non-zero, or did not answer within
// versionTimeout. Best-effort by design: this is a diagnostic, never
// something `sctx doctor` should fail over.
func VersionOf(path string) string {
	if isSelf(path) {
		return selfVersion
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// selfVersion is what VersionOf answers for the running binary itself, set
// once by main from its -ldflags version. VersionOf must NEVER execute the
// current executable: every hook installer asks for the running binary's
// version, and under `go test` the running binary is the test binary, so
// executing it re-ran the whole test package as a child until the 2s timeout
// killed it -- on Windows the killed child still held the .exe open and
// `go test` failed with "unlinkat sctx.test.exe: Access is denied" although
// every package had passed.
var selfVersion string

// SetSelfVersion records the running binary's version for VersionOf.
func SetSelfVersion(v string) { selfVersion = v }

// isSelf reports whether path names the current executable, following
// symlinks on both sides so ~/.local/bin/sctx -> /opt/homebrew/... matches.
func isSelf(path string) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	a, errA := filepath.EvalSymlinks(path)
	b, errB := filepath.EvalSymlinks(self)
	if errA != nil || errB != nil {
		return path == self
	}
	return a == b
}
