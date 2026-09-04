package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

func TestBenchCommandsForDetectsGoByGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lang, cmds := benchCommandsFor(dir)
	if lang != "go" {
		t.Fatalf("language = %q, want go", lang)
	}
	if len(cmds) != len(benchGoCommands) {
		t.Fatalf("got %d commands, want the fixed Go set (%d)", len(cmds), len(benchGoCommands))
	}
}

func TestBenchCommandsForFallsBackToGeneric(t *testing.T) {
	dir := t.TempDir()
	lang, cmds := benchCommandsFor(dir)
	if lang != "generic" {
		t.Fatalf("language = %q, want generic", lang)
	}
	if len(cmds) != len(benchGenericCommands) {
		t.Fatalf("got %d commands, want the fixed generic set (%d)", len(cmds), len(benchGenericCommands))
	}
}

// TestBenchOneNeverLeaksRawArgvIntoTheRow feeds a command whose argv carries
// a path, and asserts the returned row's Command field is the normalized,
// path-free program key — never the argv bench actually ran.
func TestBenchOneNeverLeaksRawArgvIntoTheRow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret-notes.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := chdir(t, dir)
	defer restore()

	registry := buildRegistry()
	row, err := benchOne(context.Background(), registry, config.Config{}, []string{"cat", filepath.Join(dir, "secret-notes.txt")})
	if err != nil {
		t.Fatalf("benchOne: %v", err)
	}
	if strings.Contains(row.Command, dir) || strings.Contains(row.Command, "secret-notes") {
		t.Errorf("row.Command leaked a path: %q", row.Command)
	}
	if len(row.Argv) != 0 {
		t.Errorf("row.Argv must stay empty unless the caller asks for it, got %v", row.Argv)
	}
}

// TestRunBenchTextOutputHasNoPathsWithoutVerbose runs the real generic
// command set (no --verbose) against a temp dir and asserts nothing printed
// names a filesystem path, matching the "list programs, not arguments"
// contract.
func TestRunBenchTextOutputHasNoPathsWithoutVerbose(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	stdout, restoreStdout := captureStdout(t)
	code := runBench(context.Background(), config.Config{}, nil)
	out := restoreStdout()
	if code != 0 {
		t.Fatalf("runBench exit = %d, want 0", code)
	}
	if strings.Contains(out, dir) {
		t.Errorf("bench text output leaked the working directory path:\n%s", out)
	}
	if !strings.Contains(out, "(unnamed)") {
		t.Errorf("bench text output must default the repository to (unnamed), got:\n%s", out)
	}
	_ = stdout
}

func TestParseBenchArgsRejectsUnknownFlag(t *testing.T) {
	if code := runBench(context.Background(), config.Config{}, []string{"--bogus"}); code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown flag", code)
	}
}

// chdir switches the process working directory for the duration of a test,
// restoring it afterward. Tests in this package are not parallel, so a
// process-global chdir is safe here.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(orig) }
}

// captureStdout redirects os.Stdout to a pipe for the duration of a test,
// returning a function that restores it and returns everything written.
func captureStdout(t *testing.T) (string, func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	return "", func() string {
		os.Stdout = orig
		w.Close()
		buf := make([]byte, 1<<20)
		n, _ := r.Read(buf)
		r.Close()
		return string(buf[:n])
	}
}
