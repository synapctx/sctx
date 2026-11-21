package gotestsum

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures were captured from gotestsum 1.13 driving go1.27 on macOS arm64
// against a module with passing, failing, panicking, racing, timing-out and
// non-compiling packages; absolute paths are scrubbed to /work.

func load(t *testing.T, name string) (out, errs string, exit int) {
	t.Helper()
	read := func(k string) string {
		b, err := os.ReadFile("testdata/" + name + "." + k + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if strings.TrimSpace(read("exit")) != "0" {
		exit = 1
	}
	return read("stdout"), read("stderr"), exit
}

func inputOf(t *testing.T, name string, argv ...string) format.Input {
	out, errs, exit := load(t, name)
	if len(argv) == 0 {
		argv = []string{"gotestsum"}
	}
	return format.Input{Argv: argv, Command: "gotestsum", ExitCode: exit, Stdout: strings.NewReader(out), Stderr: strings.NewReader(errs)}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "gotestsum" {
		t.Fatal("bad descriptor")
	}
}

// Every --format ends with the same summary, so the failure content must
// survive identically whichever feed preceded it.
func TestAggressiveEveryFormat(t *testing.T) {
	mustKeep := []string{
		"FAIL failpkg TestMulBad", "f_test.go:14: Mul(2,3) = 6, want 7",
		"FAIL failpkg TestTable/two", "f_test.go:22: got 4 want 5",
		"FAIL failpkg TestTable",
		"FAIL panicpkg TestBoom", "panic: assignment to entry in nil map [recovered, repanicked]",
		"example.com/ts/panicpkg.Boom(...)", "/work/go-ts/panicpkg/p.go:3", "/work/go-ts/panicpkg/p_test.go:7",
		"SKIP okpkg TestSkip: ok_test.go:18: later",
		"DONE 10 tests, 1 skipped, 4 failures",
		"stdlib frames",
	}
	for _, name := range []string{"default", "testname", "verbose", "quiet", "dots2", "testdox"} {
		t.Run(name, func(t *testing.T) {
			r, err := New().Aggressive(context.Background(), inputOf(t, name))
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range mustKeep {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, bad := range []string{"=== RUN", "testing.tRunner", "runtime/panic.go", "=== Failed", "goroutine 34 [running]:\n\ttesting"} {
				if strings.Contains(body, bad) {
					t.Errorf("kept %q", bad)
				}
			}
			out, errs, _ := load(t, name)
			if len(body) >= len(out)+len(errs) {
				t.Errorf("not smaller: %d >= %d", len(body), len(out)+len(errs))
			}
			if !r.Elided || !r.FoldStderr || !strings.Contains(body, "…+") {
				t.Errorf("elision not marked: elided=%v", r.Elided)
			}
		})
	}
}

func TestPackageLevelFailuresKeepTheirCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"build", []string{"FAIL buildpkg", "[build failed]", `buildpkg/b.go:3:23: cannot use "x" (untyped string constant) as int value in return statement`, "1 error"}},
		{"main", []string{"FAIL mainpkg", "setup failed", "mainpkg\t0.191s"}},
		{"nomatch", []string{"[setup failed]", "directory not found", "1 error"}},
		{"timeout", []string{"panic: test timed out after 1s", "TestSlow (1s)", "FAIL slowpkg TestSlow", "s_test.go:5", "goroutine 20 [sleep]:"}},
		{"race", []string{"WARNING: DATA RACE", "r_test.go:9 +0x68", "r_test.go:9 +0x78", "race detected during execution of test"}},
		{"rerun", []string{"TestMulBad (re-run 1)", "2 runs, 8 tests, 6 failures"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New().Aggressive(context.Background(), inputOf(t, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(string(r.Body), w) {
					t.Errorf("missing %q in:\n%s", w, r.Body)
				}
			}
		})
	}
}

func TestPassingRunKeepsSkipsAndCounts(t *testing.T) {
	r, err := New().Aggressive(context.Background(), inputOf(t, "okonly"))
	if err != nil {
		t.Fatal(err)
	}
	want := "SKIP okpkg TestSkip: ok_test.go:18: later\nDONE 4 tests, 1 skipped in 0.000s\n…+5 lines (live output ×5)\n"
	if string(r.Body) != want {
		t.Fatalf("got %q", r.Body)
	}
}

func TestSkipListIsCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("✓  p (1ms)\n\n=== Skipped\n")
	for i := 0; i < 8; i++ {
		sb.WriteString("=== SKIP: p TestS" + string(rune('A'+i)) + " (0.00s)\n    x_test.go:1: nope\n\n")
	}
	sb.WriteString("DONE 8 tests, 8 skipped in 0.1s\n")
	in := format.Input{Argv: []string{"gotestsum"}, Stdout: strings.NewReader(sb.String()), Stderr: strings.NewReader("")}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if strings.Count(body, "SKIP p ") != maxSkips || !strings.Contains(body, "…+3 more skipped") || !strings.Contains(body, "skips ×3") {
		t.Fatalf("skip cap wrong:\n%s", body)
	}
}

// With the failure summary suppressed the live feed is the only copy, so the
// aggressive tier must step aside and the relaxed tier keep the failure text.
func TestNoSummaryDeclinesAggressiveRelaxedKeepsFailures(t *testing.T) {
	if _, err := New().Aggressive(context.Background(), inputOf(t, "nosummary")); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err=%v", err)
	}
	r, err := New().Relaxed(context.Background(), inputOf(t, "nosummary"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, w := range []string{"f_test.go:14: Mul(2,3) = 6, want 7", "f_test.go:22: got 4 want 5", "FAIL failpkg.TestMulBad", "DONE 5 tests, 3 failures", "FAIL failpkg\n"} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in:\n%s", w, body)
		}
	}
	if strings.Contains(body, "PASS failpkg.TestMulOK") || !strings.Contains(body, "…+") {
		t.Errorf("progress handling wrong:\n%s", body)
	}
}

func TestRelaxedOnFullOutput(t *testing.T) {
	r, err := New().Relaxed(context.Background(), inputOf(t, "testname"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, w := range []string{"panic: assignment to entry in nil map", "p.go:3", "FAIL failpkg.TestMulBad", "DONE 10 tests"} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(body, "PASS okpkg.TestAdd") {
		t.Error("pass lines kept")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"gotestsum", "--version"}, {"gotestsum", "tool", "slowest"}, {"gotestsum", "--help"},
		{"gotestsum", "--format", "github-actions"}, {"gotestsum", "--format=github-actions-verbose"}, {"gotestsum", "--watch"},
	} {
		for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){New().Aggressive, New().Relaxed} {
			in := inputOf(t, "default", argv...)
			if _, err := tier(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%v: err=%v", argv, err)
			}
		}
	}
	// A flag after -- belongs to go test, not gotestsum.
	in := inputOf(t, "default", "gotestsum", "--", "-run", "tool")
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Errorf("args after -- must not decline: %v", err)
	}
	empty := format.Input{Argv: []string{"gotestsum"}, Stdout: strings.NewReader(""), Stderr: strings.NewReader("")}
	if _, err := New().Aggressive(context.Background(), empty); !errors.Is(err, format.ErrTierInapplicable) {
		t.Error("empty must decline")
	}
}

func TestCompactStack(t *testing.T) {
	in := strings.Split(strings.TrimSpace(`
goroutine 1 [chan receive]:
testing.(*T).Run(0x1)
	/usr/local/go/src/testing/testing.go:1 +0x1
main.main()
	_testmain.go:46 +0x80

goroutine 7 [running]:
example.com/x.Work()
	/work/x/x.go:10 +0x1
testing.tRunner()
	/usr/local/go/src/testing/testing.go:2 +0x2
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:3 +0x3`), "\n")
	out, n := compactStack(in)
	got := strings.Join(out, "\n")
	for _, w := range []string{"goroutine 1 [chan receive]: …+2 stdlib frames", "goroutine 7 [running]:", "example.com/x.Work()", "/work/x/x.go:10 +0x1", "…+2 stdlib frames"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if n != 4 {
		t.Errorf("dropped %d, want 4", n)
	}
}
