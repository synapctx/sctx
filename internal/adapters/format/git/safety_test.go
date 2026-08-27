package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestStatusPorcelainVariants(t *testing.T) {
	f := New()
	var short strings.Builder
	short.WriteString("## main...origin/main [ahead 2, behind 1]\n")
	for i := range statusShortEntryCap + 5 {
		fmt.Fprintf(&short, "M  staged-%02d.go\n M modified-%02d.go\nUU conflict-%02d.go\n?? new-file-%02d.go\n!! ignored-%02d.bin\n", i, i, i, i, i)
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "status", "--short", "--branch", "--ignored"}, Stdout: strings.NewReader(short.String())})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"main...origin/main [ahead 2, behind 1]", "staged (35): staged-00.go", "modified (35): modified-00.go", "conflicted (35): conflict-00.go", "untracked (35): new-file-00.go", "ignored (35): ignored-00.bin", "…+5"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}

	var v2 strings.Builder
	v2.WriteString("# branch.oid abcdef\n# branch.head main\n")
	for i := range statusShortEntryCap + 5 {
		fmt.Fprintf(&v2, "? path-%02d.txt\n", i)
	}
	out, err = f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "status", "--porcelain=v2", "--branch"}, Stdout: strings.NewReader(v2.String())})
	if err != nil {
		t.Fatalf("porcelain v2 Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "…+5 more status records") || !strings.Contains(string(out.Body), "# branch.head main") {
		t.Errorf("porcelain v2 body lost header/count: %q", out.Body)
	}
}

func TestStatusOperationAndLargeDefault(t *testing.T) {
	f := New()
	var raw strings.Builder
	raw.WriteString("interactive rebase in progress; onto abcdef0\nOn branch main\nChanges not staged for commit:\n")
	for i := range statusShortEntryCap + 4 {
		fmt.Fprintf(&raw, "\tmodified:   file-%02d.go\n", i)
	}
	raw.WriteString("Ignored files:\n\tignored.tmp\n")
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "status"}, Stdout: strings.NewReader(raw.String())})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "operation: interactive rebase in progress") || !strings.Contains(body, "modified (34)") || !strings.Contains(body, "ignored (1): ignored.tmp") || !strings.Contains(body, "…+4") {
		t.Errorf("operation/large status not retained: %q", body)
	}
}

func TestDiffPreservesStructuralSignals(t *testing.T) {
	f := New()
	raw := "diff --git a/old.bin b/new.bin\nsimilarity index 100%\nrename from old.bin\nrename to new.bin\nold mode 100644\nnew mode 100755\nBinary files a/old.bin and b/new.bin differ\n\\ No newline at end of file\n"
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"git", "diff"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	for _, want := range []string{"rename from", "rename to", "old mode", "new mode", "Binary files", "No newline"} {
		if !strings.Contains(string(out.Body), want) {
			t.Errorf("body missing %q: %q", want, out.Body)
		}
	}
}

func TestExplicitFormatsAndRecordModesDecline(t *testing.T) {
	f := New()
	tests := []struct {
		name string
		argv []string
		raw  string
	}{
		{"log custom format", []string{"git", "log", "--format=%H%n%B"}, "abc\n\nbody\n"},
		{"log patch", []string{"git", "log", "-p"}, "commit abc\ndiff --git a/a b/a\n"},
		{"binary patch", []string{"git", "diff", "--binary"}, "GIT binary patch\nliteral 1\nA\n"},
		{"combined diff", []string{"git", "show", "--cc"}, "diff --cc file\n@@@\n"},
		{"branch format", []string{"git", "branch", "--format=%(refname)%0a%(contents)"}, "refs/heads/main\nbody\n"},
		{"tag format", []string{"git", "tag", "--format=%(contents)"}, "tag body\n"},
		{"reflog format", []string{"git", "reflog", "--format=%H%n%B"}, "abc\nbody\n"},
		{"shortlog non-summary", []string{"git", "shortlog"}, "A U Thor (1):\n  subject\n"},
		{"ls-files stage", []string{"git", "ls-files", "--stage"}, "100644 abc 0\tfile\n"},
		{"worktree porcelain", []string{"git", "worktree", "list", "--porcelain"}, "worktree /repo\nHEAD abc\nbranch refs/heads/main\n\n"},
		{"blame porcelain", []string{"git", "blame", "--line-porcelain", "file"}, "abc 1 1 1\nauthor A\n\tline\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := format.Input{Argv: tt.argv, Stdout: strings.NewReader(tt.raw)}
			if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
			}
			in.Stdout = strings.NewReader(tt.raw)
			if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable && machineReadableInvocation(tt.argv) {
				t.Errorf("Relaxed() error = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestNULDelimitedOutputDeclinesRelaxed(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"git", "check-ignore", "--stdin"}, Stdout: strings.NewReader("one\x00two\x00")}
	if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestUnknownFiniteVerbUsesOnlyGenericShapes(t *testing.T) {
	f := New()
	raw := "same line\nsame line\nsame line\ndistinct\n"
	out, err := f.Relaxed(context.Background(), format.Input{Argv: []string{"git", "future-finite-verb"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if got := string(out.Body); !strings.Contains(got, "same line ×3") || !strings.Contains(got, "distinct") {
		t.Fatalf("generic fallback body = %q", got)
	}

	opaque := "hint:\n\n\nblob body\n"
	in := format.Input{Argv: []string{"git", "show", "HEAD:path.txt"}, Stdout: strings.NewReader(opaque)}
	if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("opaque blob Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestWriteFailureClassesAlwaysFallBackVerbatim(t *testing.T) {
	f := New()
	tests := []struct {
		sub, stderr string
	}{
		{"push", "remote: Permission to org/repo.git denied\nfatal: Authentication failed\n"},
		{"commit", "pre-commit hook declined the commit\n"},
		{"fetch", "remote: Repository not found.\nfatal: Could not read from remote repository.\n"},
		{"pull", "CONFLICT (content): Merge conflict in file.go\n"},
	}
	for _, tt := range tests {
		t.Run(tt.sub, func(t *testing.T) {
			in := format.Input{Argv: []string{"git", tt.sub}, Stderr: strings.NewReader(tt.stderr), ExitCode: 1}
			if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
				t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
			}
			in.Stderr = strings.NewReader(tt.stderr)
			if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
				t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
			}
		})
	}
}
