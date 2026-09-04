package binaries

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeStub writes an executable shell script at dir/name that prints
// output when called with "version".
func writeStub(t *testing.T, dir, name, versionOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub scripts are POSIX shell; not exercised on windows")
	}
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"" + versionOutput + "\"; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnPathFindsEachDistinctBinary(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeStub(t, dirA, "sctx", "sctx dev")
	writeStub(t, dirB, "sctx", "sctx 0.6.0")

	pathEnv := dirA + string(os.PathListSeparator) + dirB
	got := OnPath(pathEnv, "sctx")
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	wantFirst, err := filepath.EvalSymlinks(filepath.Join(dirA, "sctx"))
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != wantFirst {
		t.Fatalf("first entry = %q, want the one in dirA (PATH order): %q", got[0], wantFirst)
	}
}

func TestOnPathDedupesBySymlink(t *testing.T) {
	dir := t.TempDir()
	real := writeStub(t, dir, "sctx-real", "sctx 0.6.0")
	link := filepath.Join(dir, "sctx")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// PATH lists the directory once, but both names would resolve if we
	// searched for each — here we simulate two PATH entries that both
	// contain a name resolving to the same real binary.
	dir2 := t.TempDir()
	if err := os.Symlink(real, filepath.Join(dir2, "sctx")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	pathEnv := dir2 + string(os.PathListSeparator) + dir
	got := OnPath(pathEnv, "sctx")
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (both symlinks resolve to the same binary): %+v", len(got), got)
	}
	wantReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != wantReal {
		t.Fatalf("got %q, want the resolved real path %q", got[0], wantReal)
	}
}

func TestOnPathMissingDirIsSkipped(t *testing.T) {
	got := OnPath(filepath.Join(t.TempDir(), "does-not-exist"), "sctx")
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestVersionOfRunsAndTrims(t *testing.T) {
	dir := t.TempDir()
	path := writeStub(t, dir, "sctx", "sctx 0.6.0")
	if got := VersionOf(path); got != "sctx 0.6.0" {
		t.Fatalf("got %q, want %q", got, "sctx 0.6.0")
	}
}

func TestVersionOfMissingBinaryIsEmpty(t *testing.T) {
	if got := VersionOf(filepath.Join(t.TempDir(), "does-not-exist")); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// The running binary must never be executed to learn its own version: under
// `go test` that re-runs the test package and, on Windows, leaves the test
// executable locked so the cleanup fails.
func TestVersionOfNeverExecutesTheRunningBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip("no executable path:", err)
	}
	SetSelfVersion("v9.9.9-test")
	t.Cleanup(func() { SetSelfVersion("") })
	if got := VersionOf(self); got != "v9.9.9-test" {
		t.Fatalf("VersionOf(self) = %q, want the recorded self version", got)
	}
}
