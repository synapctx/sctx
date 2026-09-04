package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

type nativeGitResult struct {
	stdout, stderr []byte
	exitCode       int
}

func nativeGit(t *testing.T, dir string, args ...string) nativeGitResult {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !strings.Contains(err.Error(), "exit status") || !errorAs(err, &exitErr) {
			t.Fatalf("git %v: %v\nstderr: %s", args, err, stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return nativeGitResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exitCode: code}
}

// errorAs is kept tiny so the fixture helper remains readable without
// coupling assertions to exec.ExitError's concrete wrapping behavior.
func errorAs(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func mustNativeGit(t *testing.T, dir string, args ...string) nativeGitResult {
	t.Helper()
	r := nativeGit(t, dir, args...)
	if r.exitCode != 0 {
		t.Fatalf("git %v exited %d\nstderr: %s", args, r.exitCode, r.stderr)
	}
	return r
}

// TestNativeGitFixtures creates real repositories and feeds output from the
// installed Git CLI into the formatter. It covers unusual paths, detached
// HEAD, porcelain v2, binary diffs, worktrees and bare-repository failures.
func TestNativeGitFixtures(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo with spaces")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustNativeGit(t, repo, "init", "-b", "main")
	mustNativeGit(t, repo, "config", "user.name", "Native Fixture")
	mustNativeGit(t, repo, "config", "user.email", "fixture@example.invalid")
	tracked := filepath.Join(repo, "tracked file.txt")
	if err := os.WriteFile(tracked, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustNativeGit(t, repo, "add", "tracked file.txt")
	mustNativeGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-m", "base")

	for i := range statusShortEntryCap + 5 {
		name := filepath.Join(repo, fmt.Sprintf("untracked %02d.txt", i))
		if err := os.WriteFile(name, []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	status := mustNativeGit(t, repo, "status", "--porcelain=v2", "--branch")
	f := New()
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "status", "--porcelain=v2", "--branch"}, Stdout: bytes.NewReader(status.stdout)})
	if err != nil || !bytes.Contains(out.Body, []byte("…+5 more status records")) || !bytes.Contains(out.Body, []byte("# branch.head main")) {
		t.Fatalf("native porcelain v2 render = %q, err %v", out.Body, err)
	}

	for i := range statusShortEntryCap + 5 {
		if err := os.Remove(filepath.Join(repo, fmt.Sprintf("untracked %02d.txt", i))); err != nil {
			t.Fatal(err)
		}
	}
	mustNativeGit(t, repo, "checkout", "--detach", "HEAD")
	detached := mustNativeGit(t, repo, "status")
	out, err = f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "status"}, Stdout: bytes.NewReader(detached.stdout)})
	if err != nil || !bytes.Contains(out.Body, []byte("HEAD detached")) {
		t.Fatalf("native detached status render = %q, err %v", out.Body, err)
	}
	mustNativeGit(t, repo, "switch", "main")

	binaryPath := filepath.Join(repo, "binary data.bin")
	if err := os.WriteFile(binaryPath, []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	mustNativeGit(t, repo, "add", "binary data.bin")
	mustNativeGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-m", "add binary")
	if err := os.WriteFile(binaryPath, []byte{0, 9, 8, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	diff := mustNativeGit(t, repo, "diff", "--", "binary data.bin")
	out, err = f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "diff", "--", "binary data.bin"}, Stdout: bytes.NewReader(diff.stdout)})
	if err != nil || !bytes.Contains(out.Body, []byte("Binary files")) {
		t.Fatalf("native binary diff render = %q, err %v", out.Body, err)
	}

	worktreePath := filepath.Join(root, "worktree with spaces")
	mustNativeGit(t, repo, "worktree", "add", "-b", "fixture-worktree", worktreePath)
	worktrees := mustNativeGit(t, repo, "worktree", "list")
	// Git's own porcelain output always uses forward slashes, even on
	// Windows, so compare against the slash form rather than the OS path —
	// and case-insensitively, since Git for Windows normalizes the drive
	// letter's case independently of whatever case TEMP/os.TempDir() used.
	if !strings.Contains(strings.ToLower(string(worktrees.stdout)), strings.ToLower(filepath.ToSlash(worktreePath))) {
		t.Fatalf("native worktree list missing unusual path: %q", worktrees.stdout)
	}

	bare := filepath.Join(root, "bare repo.git")
	mustNativeGit(t, root, "init", "--bare", bare)
	failure := nativeGit(t, root, "-C", bare, "status")
	if failure.exitCode == 0 || len(failure.stderr) == 0 {
		t.Fatalf("bare status unexpectedly succeeded: %+v", failure)
	}
	in := format.Input{Argv: []string{"git", "-C", bare, "status"}, Stdout: bytes.NewReader(failure.stdout), Stderr: bytes.NewReader(failure.stderr), ExitCode: failure.exitCode}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("bare failure aggressive error = %v", err)
	}
	in.Stdout, in.Stderr = bytes.NewReader(failure.stdout), bytes.NewReader(failure.stderr)
	if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("bare failure relaxed error = %v", err)
	}
}
