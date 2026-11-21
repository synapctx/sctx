package cargo

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures in testdata/ were captured from a real cargo 1.100 / nextest 0.9
// toolchain (macOS arm64); absolute paths are scrubbed to /work.

func fixture(t *testing.T, name, stream string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + "." + stream + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func inputFor(t *testing.T, name string, argv ...string) format.Input {
	t.Helper()
	return format.Input{
		Argv: argv, Command: "cargo " + argv[1],
		Stdout: strings.NewReader(fixture(t, name, "stdout")),
		Stderr: strings.NewReader(fixture(t, name, "stderr")),
	}
}

func rawSize(t *testing.T, name string) int {
	return len(fixture(t, name, "stdout")) + len(fixture(t, name, "stderr"))
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "cargo" {
		t.Fatal("bad descriptor")
	}
}

func TestAggressiveFixtures(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		want    []string // must appear in the render
		dropped []string // must not appear
		smaller bool
	}{
		{"build-ok", []string{"cargo", "build"},
			[]string{"Finished dev in 1.05s", "compiling ×2", "downloaded ×1"},
			[]string{"Compiling", "Downloaded"}, true},
		{"build-warn", []string{"cargo", "build"},
			[]string{"src/main.rs:1:29: warning: unused variable: `a` [unused_variables]", "generated 1 warning", "diag detail ×6"},
			[]string{"|", "help: if this is intentional"}, true},
		{"test-ok", []string{"cargo", "test"},
			[]string{"cargo test: 4 passed, 0 failed, 1 ignored", "test ok ×4", "test ignored ×1"},
			[]string{"Running unittests", "adds ... ok"}, true},
		{"test-fail", []string{"cargo", "test"},
			[]string{
				"cargo test: 1 passed, 4 failed",
				"---- tests::bad_sum stdout ----", "panicked at src/lib.rs:25:9", "left: 5", "right: 6", "sum was wrong",
				"---- tests::bad_unwrap stdout ----", "ParseIntError { kind: InvalidDigit }", "src/lib.rs:30:28",
				"---- tests::bad_panic stdout ----", "boom",
				"---- src/lib.rs - add (line 3) stdout ----", "left: 3", "right: 4",
				"error: test failed, to rerun pass `--lib`", "error: doctest failed, to rerun pass `--doc`",
				"error: 2 targets failed:", "`--doc`", "unused dependency `itoa`", "backtrace hint ×2",
			},
			[]string{"Compiling", "(32467418)", "RUST_BACKTRACE"}, true},
		{"test-quiet", []string{"cargo", "test", "-q"},
			[]string{"cargo test: 1 passed, 4 failed", "---- tests::bad_panic stdout ----", "left: 5", "right: 6"},
			[]string{"--- FAILED", "3/4"}, true},
		{"test-bt", []string{"cargo", "test"},
			[]string{"demo::tests::bad_sum", "at ./src/lib.rs:25:9", "backtrace frames", "left: 5"},
			[]string{"rust_begin_unwind", "panicking.rs", "/rustc/"}, true},
		{"ws-test", []string{"cargo", "test", "--workspace"},
			[]string{"cargo test: 2 passed, 1 failed", "---- t::bad stdout ----", "b/src/lib.rs:5:24", "left: 2", "right: 3", "-p wb --lib"},
			nil, true},
		{"clippy", []string{"cargo", "clippy"},
			[]string{
				"src/lib.rs:2:5: warning: unneeded `return` statement [clippy::needless_return]",
				"src/lib.rs:6:5: warning: unneeded `return` statement [clippy::needless_return]",
				"src/lib.rs:9:13: warning: writing `&Vec` instead of `&[_]` involves a new object where a slice will do [clippy::ptr_arg]",
				"src/lib.rs:11:14: warning: the loop variable `i` is only used to index `v` [clippy::needless_range_loop]",
				"src/lib.rs:18:18: warning: this `if` has identical blocks [clippy::if_same_then_else]",
				"src/lib.rs:18:8: warning: equality checks against true are unnecessary [clippy::bool_comparison]",
				"generated 6 warnings", "diag detail ×71",
			},
			[]string{"rust-clippy", "^^^^"}, true},
		{"clippy-deny", []string{"cargo", "clippy", "--", "-D", "warnings"},
			[]string{
				"src/lib.rs:2:5: error: unneeded `return` statement [clippy::needless_return]",
				"src/lib.rs:18:8: error: equality checks against true are unnecessary [clippy::bool_comparison]",
				"error: could not compile `demo` (lib) due to 6 previous errors",
			},
			[]string{"help: remove `return`"}, true},
		{"doc", []string{"cargo", "doc"},
			[]string{"Finished dev in 0.55s", "Generated /work/rs-ok/target/doc/demo/index.html", "documenting ×2"},
			[]string{"Documenting"}, true},
		{"run", []string{"cargo", "run"},
			[]string{"sum=5", "running ×1"}, []string{"Running `target"}, true},
		{"install", []string{"cargo", "install", "--path", "."},
			[]string{"Installing /work/inst-root/bin/demo", "warning: be sure to add", "compiling ×1"},
			[]string{"Compiling", "Installed package"}, true},
		{"nx-ok", []string{"cargo", "nextest", "run"},
			[]string{"Summary [   0.011s] 3 tests run: 3 passed, 1 skipped", "pass/skip ×3"},
			[]string{"PASS ["}, true},
		{"nx-fail", []string{"cargo", "nextest", "run", "--no-fail-fast"},
			[]string{
				"FAIL demo tests::bad_sum", "FAIL demo tests::bad_panic", "FAIL demo tests::bad_unwrap",
				"panicked at src/lib.rs:25:9", "left: 5", "right: 6", "boom", "InvalidDigit",
				"Summary [   0.014s] 4 tests run: 1 passed, 3 failed, 0 skipped", "error: test run failed", "recap ×3",
			},
			[]string{"test result: FAILED", "running 1 test", "PASS ["}, true},
		{"nx-ws", []string{"cargo", "nextest", "run"},
			[]string{"FAIL wb t::bad", "b/src/lib.rs:5:24", "left: 2", "right: 3", "3 tests run: 2 passed, 1 failed"},
			nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New().Aggressive(context.Background(), inputFor(t, tc.name, tc.argv...))
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, d := range tc.dropped {
				if strings.Contains(body, d) {
					t.Errorf("kept noise %q in:\n%s", d, body)
				}
			}
			if tc.smaller && len(body) >= rawSize(t, tc.name) {
				t.Errorf("not smaller: %d >= %d", len(body), rawSize(t, tc.name))
			}
			if !r.FoldStderr {
				t.Error("stderr must be folded")
			}
			if strings.Contains(body, "…+") != r.Elided {
				t.Errorf("Elided=%v disagrees with marker presence", r.Elided)
			}
		})
	}
}

// Compile errors are the failure signal itself: every diagnostic byte stays.
func TestCompileErrorsVerbatim(t *testing.T) {
	for _, name := range []string{"build-cerr", "check-cerr"} {
		t.Run(name, func(t *testing.T) {
			sub := strings.SplitN(name, "-", 2)[0]
			errs := fixture(t, name, "stderr")
			r, err := New().Aggressive(context.Background(), inputFor(t, name, "cargo", sub))
			if err != nil {
				t.Fatal(err)
			}
			start := strings.Index(errs, "error[E0425]")
			if !strings.Contains(string(r.Body), strings.TrimRight(errs[start:], "\n")) {
				t.Fatalf("diagnostics not verbatim:\n%s", r.Body)
			}
			if strings.Contains(string(r.Body), "Compiling") {
				t.Error("progress kept")
			}
		})
	}
}

func TestRunStdoutIsNeverInterpreted(t *testing.T) {
	prog := "test x ... ok\nerror: not a diagnostic\ntest result: ok. 1 passed; 0 failed; 0 ignored\n\n"
	in := format.Input{
		Argv:   []string{"cargo", "run"},
		Stdout: strings.NewReader(prog),
		Stderr: strings.NewReader(fixture(t, "run", "stderr")),
	}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), strings.TrimRight(prog, "\n")) {
		t.Fatalf("program output altered:\n%s", r.Body)
	}
}

func TestFmtCheck(t *testing.T) {
	raw := fixture(t, "fmt-bad", "stdout")
	if !strings.Contains(raw, "\x1b[") {
		t.Fatal("fixture lost its colour escapes")
	}
	in := inputFor(t, "fmt-bad", "cargo", "fmt", "--check")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, w := range []string{"1 hunk(s) in 1 file(s)", "/work/rs-clippy/src/lib.rs:15", "-    if x == true { 1 } else { 1 }", "+    } else {", "context ×5"} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in:\n%s", w, body)
		}
	}
	if strings.Contains(body, "\x1b") {
		t.Error("escape left in render")
	}
}

func TestFmtLargeDiffCollapsesToLocations(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("Diff in /work/src/a.rs:" + string(rune('1'+i%9)) + "0:\n ctx\n-a\n-b\n+c\n+d\n ctx\n")
	}
	in := format.Input{Argv: []string{"cargo", "fmt", "--check"}, Stdout: strings.NewReader(sb.String()), Stderr: strings.NewReader("")}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if !strings.Contains(body, "30 hunk(s) in 1 file(s)") || !strings.Contains(body, "+2 -2 lines") || !strings.Contains(body, "collapsed hunk lines ×") {
		t.Fatalf("budget collapse missing:\n%s", body)
	}
	if strings.Count(body, "/work/src/a.rs:") != 30 {
		t.Error("a hunk location was lost")
	}
}

func TestRelaxedKeepsEverythingElse(t *testing.T) {
	r, err := New().Relaxed(context.Background(), inputFor(t, "test-fail", "cargo", "test"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, want := range []string{"test result: FAILED", "test tests::bad_sum ... FAILED", "left: 5", "warning: unused dependency", "error: doctest failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Compiling") || strings.Contains(body, "adds ... ok") {
		t.Error("noise kept")
	}
	r, err = New().Relaxed(context.Background(), inputFor(t, "nx-fail", "cargo", "nextest", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Body), "PASS [") || !strings.Contains(string(r.Body), "FAIL [") {
		t.Errorf("nextest relaxed wrong:\n%s", r.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"cargo", "add"}, {"cargo", "tree"}, {"cargo", "test", "--", "--list"},
		{"cargo", "build", "--message-format=json"}, {"cargo", "test", "--", "--format", "json"},
		{"cargo", "nextest", "list"}, {"cargo", "nextest", "run", "--message-format", "libtest-json"},
		{"cargo", "build", "--help"},
	} {
		for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){New().Aggressive, New().Relaxed} {
			in := format.Input{Argv: argv, Stdout: strings.NewReader("   Compiling x v1\n"), Stderr: strings.NewReader("")}
			if _, err := tier(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%v: err=%v", argv, err)
			}
		}
	}
	mk := func(out, errs string) format.Input {
		return format.Input{Argv: []string{"cargo", "build"}, Stdout: strings.NewReader(out), Stderr: strings.NewReader(errs)}
	}
	for name, in := range map[string]format.Input{
		"empty":   mk("", ""),
		"unknown": mk("hello\n", ""),
		"colour":  mk("", "\x1b[1m\x1b[32m   Compiling\x1b[0m x v1\n"),
	} {
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s must decline, err=%v", name, err)
		}
	}
	in := format.Input{Argv: []string{"cargo", "fmt"}, Stdout: strings.NewReader("not a diff\n"), Stderr: strings.NewReader("")}
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("fmt without diffs must decline, err=%v", err)
	}
}

func TestSubcommandRouting(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"cargo", "+nightly", "test"}, "test"},
		{[]string{"cargo", "--manifest-path", "x/Cargo.toml", "build"}, "build"},
		{[]string{"cargo", "nextest", "run", "-p", "x"}, "nextest"},
		{[]string{"cargo", "nextest", "list"}, "nextest-other"},
		{[]string{"cargo", "clippy", "--", "-D", "warnings"}, "clippy"},
	} {
		if got := dispatch(tc.argv); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.argv, got, tc.want)
		}
	}
}
