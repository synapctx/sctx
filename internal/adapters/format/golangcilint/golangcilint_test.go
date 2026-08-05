package golangcilint

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "golangci-lint" {
		t.Errorf("Command = %q, want %q", got.Command, "golangci-lint")
	}
}

func TestNonRunSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"golangci-lint", "cache", "status"},
		Stdout: strings.NewReader("some output\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

const eightIssueFixture = `pkg/a/a.go:10:2: unused variable x (unused)
pkg/a/a.go:22:5: should not use dot imports (revive)
	import . "fmt"
	       ^
pkg/a/a.go:40:1: exported function Foo should have comment (revive)
pkg/b/b.go:5:10: ineffectual assignment to err (ineffassign)
	err = doSomething()
	^
pkg/b/b.go:15:3: G104: Errors unhandled (gosec)
pkg/b/b.go:16:3: G104: Errors unhandled (gosec)
pkg/c/c.go:1:1: package comment should be of the form "Package c ..." (revive)
pkg/c/c.go:99:4: cyclomatic complexity 15 of func High is high (gocyclo)
`

func TestAggressiveRunEightIssuesThreeFiles(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"golangci-lint", "run", "./..."},
		Stdout: strings.NewReader(eightIssueFixture),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "pkg/a/a.go — 3 issues") {
		t.Errorf("missing file group: %q", body)
	}
	if !strings.Contains(body, "L22:5 should not use dot imports (revive) …+2 context") {
		t.Errorf("missing context marker: %q", body)
	}
	if !strings.Contains(body, "L15:3 G104: Errors unhandled (gosec)") ||
		strings.Contains(body, "L15:3 G104: Errors unhandled (gosec) …") {
		t.Errorf("issue without context wrongly got a marker: %q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "8 issues in 3 files") {
		t.Errorf("missing final summary: %q", body)
	}
}

func TestExitOneStillFormats(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"golangci-lint", "run"},
		Stdout:   strings.NewReader("pkg/a/a.go:1:1: unused variable x (unused)\n"),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v, want nil (exit 1 is the normal issues-found case)", err)
	}
	if !strings.Contains(string(out.Body), "1 issues in 1 files") {
		t.Errorf("body = %q", out.Body)
	}
}

func TestExitTwoDegrades(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"golangci-lint", "run"},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("level=error msg=\"failed to load config\"\n"),
		ExitCode: 2,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestZeroParseableIssuesInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader("0 issues.\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
