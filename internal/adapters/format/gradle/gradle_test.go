package gradle

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func render(t *testing.T, name, cwd string, argv ...string) (string, string, error) {
	t.Helper()
	raw := fixture(t, name)
	f := newFormatter("gradle")
	f.getwd = func() string { return cwd }
	r, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)})
	return raw, string(r.Body), err
}

func TestAggressiveFixtures(t *testing.T) {
	tests := []struct {
		fixture, cwd string
		want         []string
		notWant      []string
		maxRatio     float64
	}{
		{"gr-build-ok-cold", "/work/gr-ok", []string{"BUILD SUCCESSFUL in", "4 actionable tasks: 4 executed", "task lines"}, []string{"> Task", "Starting a Gradle Daemon", "configuration cache"}, 0.3},
		{"gr-build-ok-warm", "/work/gr-ok", []string{"BUILD SUCCESSFUL in", "4 actionable tasks: 4 up-to-date"}, []string{"UP-TO-DATE"}, 0.3},
		{"gr-test-ok-rerun", "/work/gr-ok", []string{"BUILD SUCCESSFUL in"}, []string{"> Task"}, 0.5},
		{"gr-test-fail", "/work/gr-fail", []string{
			"FAILED :test", "CalcTest > adds() FAILED", "AssertionFailedError at CalcTest.java:5",
			"CalcTest > divs() FAILED", "ArithmeticException at CalcTest.java:6", "CalcTest > nulls() FAILED",
			"4 tests completed, 3 failed", "There were failing tests. See the report at: build/reports/tests/test/index.html",
			"BUILD FAILED in", "3 actionable tasks: 3 executed"}, []string{"* Try:", "--scan", "file://"}, 0.8},
		{"gr-test-fail-full", "/work/gr-full", []string{
			"sum is wrong ==> expected: <6> but was: <5>", "at app//demo.CalcTest.adds(CalcTest.java:5)",
			"java.lang.ArithmeticException: / by zero", "at demo.Calc.div(Calc.java:4)", "at demo.CalcTest.divs(CalcTest.java:6)",
			"expected: not <null>", "framework frames", "BUILD FAILED"}, []string{"at app//org.junit."}, 0.5},
		{"gr-compile-fail", "/work/gr-compile", []string{
			"src/main/java/demo/Calc.java:3: error: cannot find symbol", "symbol:   variable c",
			"src/main/java/demo/Calc.java:4: error: incompatible types", "src/main/java/demo/Calc.java:5: error: cannot find symbol",
			"symbol:   method undefined()", "3 errors", "What went wrong:", "Compilation failed; see the compiler output below.", "BUILD FAILED"}, []string{"Try:"}, 0.6},
		{"gr-dep-fail", "/work/gr-dep", []string{
			"Could not find com.nonexistent:nothing:9.9.9.", "nothing-9.9.9.pom", "Required by:", "root project 'app'", "FAILED :compileJava", "BUILD FAILED"}, []string{"--stacktrace", "Get more help"}, 0.7},
		{"gr-multi-fail", "/work/gr-multi", []string{
			"core/src/main/java/demo/coreSvc.java:3: warning: [rawtypes] found raw type: List",
			"web/src/test/java/demo/SvcTest.java:4: warning: [deprecation]", "FAILED :web:test", "SvcTest > a() FAILED",
			"There were failing tests. See the report at: web/build/reports/tests/test/index.html", "12 actionable tasks: 12 executed", "BUILD FAILED"},
			[]string{"missing type arguments", "^"}, 0.4},
		{"gr-wrapper-cold", "/work/gr-wrap", []string{"BUILD SUCCESSFUL in 4s"}, []string{"10%", "Downloading"}, 0.3},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			raw, got, err := render(t, tc.fixture, tc.cwd, "gradle", "build")
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in:\n%s", w, got)
				}
			}
			if ratio := float64(len(got)) / float64(len(raw)); ratio > tc.maxRatio {
				t.Errorf("render is %.0f%% of raw, want <= %.0f%%", ratio*100, tc.maxRatio*100)
			}
			if !strings.Contains(got, "…+") && !strings.Contains(got, "×") {
				t.Errorf("no elision marker:\n%s", got)
			}
		})
	}
}

// A failed build keeps the failing task, every compiler error and the
// "What went wrong" text.
func TestFailuresNeverLoseErrors(t *testing.T) {
	cwds := map[string]string{"gr-test-fail": "/work/gr-fail", "gr-compile-fail": "/work/gr-compile", "gr-dep-fail": "/work/gr-dep", "gr-multi-fail": "/work/gr-multi"}
	for name, cwd := range cwds {
		t.Run(name, func(t *testing.T) {
			raw, got, err := render(t, name, cwd, "gradle", "build")
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range strings.Split(raw, "\n") {
				switch {
				case strings.Contains(l, ": error: "), strings.HasSuffix(l, " FAILED"), strings.HasPrefix(l, "BUILD FAILED"):
					want := strings.ReplaceAll(strings.TrimSpace(l), "/work/"+filepath.Base(cwd)+"/", "")
					if rest, ok := strings.CutPrefix(want, "> Task "); ok {
						want = "FAILED " + strings.TrimSuffix(rest, " FAILED")
					}
					if !strings.Contains(got, want) {
						t.Errorf("line lost: %q", l)
					}
				}
			}
		})
	}
}

func TestGolden(t *testing.T) {
	for name, cwd := range map[string]string{"gr-test-fail-full": "/work/gr-full", "gr-compile-fail": "/work/gr-compile", "gr-multi-fail": "/work/gr-multi"} {
		t.Run(name, func(t *testing.T) {
			_, got, err := render(t, name, cwd, "gradle", "build")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != got {
				t.Errorf("golden mismatch; run with -update\n%s", got)
			}
		})
	}
}

func TestInapplicable(t *testing.T) {
	f := newFormatter("gradle")
	for _, tc := range []struct {
		name  string
		argv  []string
		input string
	}{
		{"info flag", []string{"gradle", "--info", "build"}, fixture(t, "gr-test-fail")},
		{"debug flag", []string{"gradle", "-d", "build"}, fixture(t, "gr-test-fail")},
		{"version", []string{"gradle", "--version"}, "Gradle 9.9.2\n"},
		{"no footer (missing java)", []string{"./gradlew", "build"}, fixture(t, "gr-wrapper-nojava")},
		{"empty", []string{"gradle", "-q", "build"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(tc.input)})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRelaxedDropsWrapperProgress(t *testing.T) {
	raw := "Downloading https://services.gradle.org/distributions/gradle-9.9.2-bin.zip\n............10%.............20%.......\n.....\nfoo\n"
	r, err := newFormatter("gradlew").Relaxed(context.Background(), format.Input{Argv: []string{"./gradlew", "-q"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	got := string(r.Body)
	if strings.Contains(got, "10%") || !strings.Contains(got, "foo") || !strings.Contains(got, "…+3 download lines elided") || !r.Elided {
		t.Errorf("unexpected relaxed render: %q", got)
	}
}

func TestDescriptors(t *testing.T) {
	var got []string
	for _, f := range All() {
		got = append(got, f.Descriptor().Command)
	}
	if strings.Join(got, ",") != "gradle,gradlew" {
		t.Fatalf("commands = %v", got)
	}
}
