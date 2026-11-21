package bazel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures were captured from elixir:1.17 (gcc:14 + bazel 7.4.1 (linux-arm64).
func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(e)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s mismatch\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func forCmd(c string) *Formatter { return &Formatter{command: c} }

func TestRendersKeepErrorSignal(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		keep []string
		drop []string
	}{
		{"bz-build-err", []string{"bazel", "build", "//pkg:bad"},
			[]string{"ERROR: /work/proj/pkg/BUILD:1:8: Executing genrule //pkg:bad failed: (Exit 3): bash failed: error executing Genrule command (from target //pkg:bad)",
				"oops", "Target //pkg:bad failed to build", "ERROR: Build did NOT complete successfully", "INFO: Elapsed time:"},
			[]string{"Computing main repo mapping", "Analyzing:", "Use --verbose_failures", "Use --sandbox_debug", "source external/bazel_tools"}},
		{"bz-build-cc-err", []string{"bazel", "build", "//pkg:lib"},
			[]string{"ERROR: /work/proj/pkg/BUILD:2:11: Compiling pkg/lib.cc failed: (Exit 1): gcc failed: error executing CppCompile command (from target //pkg:lib)",
				"pkg/lib.cc:1:18: error: 'nope' was not declared in this scope", "int f() { return nope; }", "ERROR: Build did NOT complete successfully"},
			[]string{"Loading:", "[1 / 1] no actions running", "-fstack-protector"}},
		{"bz-test-mixed", []string{"bazel", "test", "//pkg:all"},
			[]string{"FAIL: //pkg:fail_test (see /home/user/.cache/bazel/_bazel_root/0123456789abcdef0123456789abcdef/execroot/_main/bazel-out/aarch64-fastbuild/testlogs/pkg/fail_test/test.log)",
				"INFO: Build completed, 1 test FAILED, 11 total actions", "//pkg:pass_test", "PASSED in 0.0s", "//pkg:fail_test", "FAILED in 0.0s", "Executed 2 out of 2 tests: 1 test passes and 1 fails locally."},
			[]string{"Analyzing:", "There were tests whose specified size", "INFO: 11 processes"}},
		{"bz-build-ok", []string{"bazel", "build", "//pkg:gen2"},
			[]string{"Target //pkg:gen2 up-to-date:", "bazel-bin/pkg/gen2.txt", "INFO: Build completed successfully, 3 total actions"},
			[]string{"Extracting Bazel installation", "Analyzing:", "Loading:"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := forCmd("bazel").Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("render %d >= raw %d", len(body), len(raw))
			}
			if !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			// Every non-progress ERROR/FAIL line keeps at least its first 150 bytes.
			for _, l := range strings.Split(raw, "\n") {
				if strings.HasPrefix(l, "ERROR: ") || strings.HasPrefix(l, "FAIL: ") {
					head := l
					if len(head) > 150 {
						head = head[:150]
					}
					if !strings.Contains(body, head) && !strings.Contains(body, strings.TrimSpace(strings.SplitN(l, " (from target", 2)[0])) {
						t.Errorf("error line lost: %q", l)
					}
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := forCmd("bazel").Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(rel.Body), "…+") {
				t.Error("relaxed elision not marked")
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "bz-test-mixed")
	for _, argv := range [][]string{
		{"bazel"}, {"bazel", "run", "//x"}, {"bazel", "query", "//..."}, {"bazel", "cquery", "x"}, {"bazel", "info"}, {"bazel", "--help"}, {"bazel", "build", "--help"}, {"bazel", "version"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := forCmd("bazel").Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}

func TestStartupFlagsBeforeCommand(t *testing.T) {
	raw, code := load(t, "bz-build-ok")
	in := format.Input{Argv: []string{"bazel", "--output_user_root=/x", "build", "//pkg:gen2"}, Stdout: strings.NewReader(raw), ExitCode: code}
	if _, err := forCmd("bazel").Aggressive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestAllCoversLaunchers(t *testing.T) {
	got := map[string]bool{}
	for _, f := range All() {
		got[f.Descriptor().Command] = true
	}
	if !got["bazel"] || !got["bazelisk"] {
		t.Errorf("got %v", got)
	}
}
