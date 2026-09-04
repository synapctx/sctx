package makefmt

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// goTestFailFixture is `make test` output captured from a real `go test`
// run across four packages (darwin/arm64, this platform): recipe echo, one
// failing package and three passing (cached) ones, exit 2, make's own
// "*** Error 1" banner on a separate stream.
const goTestFailFixtureStdout = `go test ./...
--- FAIL: TestAddBroken (0.00s)
    fixture_test.go:14: Add(2,2) = 4, want 5
FAIL
FAIL	fixture	0.237s
ok  	fixture/pkg1	(cached)
ok  	fixture/pkg2	(cached)
ok  	fixture/pkg3	(cached)
FAIL
`

const goTestFailFixtureStderr = "make: *** [test] Error 1\n"

func TestAggressiveDelegatesGoTestRegionAndKeepsFailureOnNonZeroExit(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"make", "test"},
		Stdout:   strings.NewReader(goTestFailFixtureStdout),
		Stderr:   strings.NewReader(goTestFailFixtureStderr),
		ExitCode: 2,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "go test ./...") {
		t.Errorf("recipe echo dropped: %q", body)
	}
	if !strings.Contains(body, "TestAddBroken") {
		t.Errorf("failing test name dropped: %q", body)
	}
	if !strings.Contains(body, "Add(2,2) = 4, want 5") {
		t.Errorf("failure detail dropped: %q", body)
	}
	if !strings.Contains(body, "Error 1") {
		t.Errorf("make's own error banner dropped: %q", body)
	}
	raw := goTestFailFixtureStdout + goTestFailFixtureStderr
	if len(out.Body) >= len(raw) {
		t.Errorf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(raw))
	}
	t.Logf("make test (go test FAIL): %d -> %d bytes", len(raw), len(out.Body))
}

// goTestPassFixture is a larger real `go test ./... -v` run (captured from
// this repository's sed+gofmt packages, concatenated) behind a `go test`
// recipe echo, to show the delegation actually compresses a passing run too.
const goTestPassFixtureStdout = `go test ./...
=== RUN   TestRecognizedShapesDelegateToRead
=== RUN   TestRecognizedShapesDelegateToRead/range_address
=== RUN   TestRecognizedShapesDelegateToRead/regex_address
--- PASS: TestRecognizedShapesDelegateToRead (0.00s)
    --- PASS: TestRecognizedShapesDelegateToRead/range_address (0.00s)
    --- PASS: TestRecognizedShapesDelegateToRead/regex_address (0.00s)
=== RUN   TestUnrecognizedShapesDecline
=== RUN   TestUnrecognizedShapesDecline/substitution
=== RUN   TestUnrecognizedShapesDecline/in-place_edit
--- PASS: TestUnrecognizedShapesDecline (0.00s)
    --- PASS: TestUnrecognizedShapesDecline/substitution (0.00s)
    --- PASS: TestUnrecognizedShapesDecline/in-place_edit (0.00s)
=== RUN   TestNonZeroExitDeclinesToVerbatim
--- PASS: TestNonZeroExitDeclinesToVerbatim (0.00s)
PASS
ok  	github.com/synapctx/sctx/internal/adapters/format/sed	(cached)
`

func TestAggressiveDelegatesGoTestRegionOnPassingRun(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"make", "test"},
		Stdout: strings.NewReader(goTestPassFixtureStdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "go test ./...") {
		t.Errorf("recipe echo dropped: %q", body)
	}
	if len(out.Body) >= len(goTestPassFixtureStdout) {
		t.Errorf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(goTestPassFixtureStdout))
	}
	t.Logf("make test (go test PASS): %d -> %d bytes", len(goTestPassFixtureStdout), len(out.Body))
}

// golangciFixture is `make lint` output captured from a real golangci-lint
// run (darwin/arm64): recipe echo, three "unused" issues (each with its own
// source-line + caret excerpt), exit 2.
const golangciFixtureStdout = `golangci-lint run
unused.go:3:6: func neverCalled is unused (unused)
func neverCalled() int {
     ^
unused.go:7:6: func alsoNeverCalled is unused (unused)
func alsoNeverCalled() int {
     ^
unused.go:11:6: func thirdNeverCalled is unused (unused)
func thirdNeverCalled() int {
     ^
3 issues:
* unused: 3
`

const golangciFixtureStderr = "make: *** [lint] Error 1\n"

func TestAggressiveDelegatesGolangciLintRegion(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"make", "lint"},
		Stdout:   strings.NewReader(golangciFixtureStdout),
		Stderr:   strings.NewReader(golangciFixtureStderr),
		ExitCode: 2,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "golangci-lint run") {
		t.Errorf("recipe echo dropped: %q", body)
	}
	if !strings.Contains(body, "unused.go") {
		t.Errorf("issue location dropped: %q", body)
	}
	if !strings.Contains(body, "Error 1") {
		t.Errorf("make's own error banner dropped: %q", body)
	}
	raw := golangciFixtureStdout + golangciFixtureStderr
	if len(out.Body) >= len(raw) {
		t.Errorf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(raw))
	}
	t.Logf("make lint (golangci-lint): %d -> %d bytes", len(raw), len(out.Body))
}

// TestNonDelegatableRecipeStillCollapsesAsBefore pins that a recipe that
// isn't a bare go/golangci-lint invocation (here, piped through `tee`) is
// NOT delegated — classifyRecipe only recognises a bare invocation — and
// still gets make's own line-level collapsing, unchanged from before this
// file existed.
func TestNonDelegatableRecipeStillCollapsesAsBefore(t *testing.T) {
	f := New()
	stdout := "go test ./... | tee log.txt\n" +
		"ok  \tfixture\t0.01s\n" +
		"ok  \tfixture\t0.01s\n" +
		"ok  \tfixture\t0.01s\n"
	in := format.Input{
		Argv:   []string{"make", "test"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "×3") {
		t.Errorf("expected make's own duplicate-line collapse, got: %q", out.Body)
	}
}
