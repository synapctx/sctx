package maven

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
	f := newFormatter("mvn")
	f.getwd = func() string { return cwd }
	r, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)})
	return raw, string(r.Body), err
}

func TestAggressiveFixtures(t *testing.T) {
	tests := []struct {
		fixture, cwd string
		argv         []string
		want         []string
		notWant      []string
		maxRatio     float64
	}{
		{"mvn-test-ok-cold", "/work/mvn-ok", []string{"mvn", "test"},
			[]string{"app 1.0 (", "surefire:test", "Tests run: 2, Failures: 0", "BUILD SUCCESS", "Total time:", "download"},
			[]string{"Downloading", "Progress ("}, 0.02},
		{"mvn-test-ok-warm", "/work/mvn-ok", []string{"mvn", "test"},
			[]string{"Tests run: 2", "BUILD SUCCESS"}, []string{"skip non existing", "Scanning for projects"}, 0.25},
		{"mvn-test-fail", "/work/mvn-fail", []string{"mvn", "test"},
			[]string{
				"FAIL demo.CalcTest.adds", "sum is wrong ==> expected: <6> but was: <5>", "at demo.CalcTest.adds(CalcTest.java:5)",
				"ERROR demo.CalcTest.divs", "java.lang.ArithmeticException: / by zero", "at demo.Calc.div(Calc.java:4)",
				"FAIL demo.CalcTest.nulls", "expected: not <null>", "at demo.CalcTest.nulls(CalcTest.java:8)",
				"Tests run: 4, Failures: 2, Errors: 1", "BUILD FAILURE", "Failed to execute goal", "surefire-reports",
			},
			[]string{"at org.junit.", "-> [Help 1]", "Re-run Maven"}, 0.3},
		{"mvn-compile-fail", "/work/mvn-compile", []string{"mvn", "compile"},
			[]string{
				"src/main/java/demo/Calc.java:[3,47] cannot find symbol", "symbol:   variable c",
				"src/main/java/demo/Calc.java:[4,48] incompatible types: int cannot be converted to java.lang.String",
				"src/main/java/demo/Calc.java:[5,16] cannot find symbol", "symbol:   method undefined()",
				"3 errors", "BUILD FAILURE", "Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin",
			},
			[]string{"[Help 1]"}, 0.35},
		{"mvn-dep-fail", "/work/mvn-dep", []string{"mvn", "compile"},
			[]string{"Could not resolve dependencies", "com.nonexistent:nothing:jar:9.9.9", "NOT allowed from central", "BUILD FAILURE", "The POM for com.nonexistent"},
			[]string{"[Help 1]"}, 0.5},
		{"mvn-dep-transfer", "/work/mvn-dep2", []string{"mvn", "compile"},
			[]string{"Could not find artifact org.junit.jupiter:junit-jupiter-api:jar:99.9.9", "BUILD FAILURE"}, nil, 0.5},
		{"mvn-multi-fail", "/work/mvn-multi", []string{"mvn", "test"},
			[]string{
				"parent 1.0 [1/4]", "core 1.0 [2/4]", "web 1.0 [3/4]",
				"Reactor: cli SKIPPED; parent SUCCESS 0.987s; core SUCCESS 0.998s; web FAILURE 0.842s",
				"FAIL demo.SvcTest.a", "expected: <2> but was: <1>", "at demo.SvcTest.a(SvcTest.java:4)",
				"core/src/main/java/demo/coreSvc.java:[3,72] found raw type", "×3", "-rf :web", "BUILD FAILURE",
			},
			[]string{"Downloading", "Progress ("}, 0.25},
		{"mvn-multi-failatend", "/work/mvn-multi", []string{"mvn", "test", "-fae"},
			[]string{"FAIL demo.SvcTest.a", "BUILD FAILURE", "Reactor:"}, nil, 0.4},
		{"mvn-tree", "/work/mvn-ok", []string{"mvn", "dependency:tree"},
			[]string{"demo:app:jar:1.0", `\- org.junit.jupiter:junit-jupiter:jar:5.10.2:test`, "org.opentest4j:opentest4j:jar:1.3.0:test", "BUILD SUCCESS"},
			[]string{"Downloading"}, 0.02},
		{"mvn-package-ok", "/work/mvn-ok", []string{"mvn", "package"},
			[]string{"Building jar: target/app-1.0.jar", "BUILD SUCCESS"}, nil, 0.02},
		{"mvn-verify-ok", "/work/mvn-ok", []string{"mvn", "clean", "verify"},
			[]string{"BUILD SUCCESS"}, nil, 0.3},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			raw, got, err := render(t, tc.fixture, tc.cwd, tc.argv...)
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

// Every distinct [ERROR] diagnostic of a failing build survives rendering.
func TestFailuresNeverLoseErrors(t *testing.T) {
	for _, name := range []string{"mvn-test-fail", "mvn-compile-fail", "mvn-dep-fail", "mvn-dep-transfer", "mvn-multi-fail"} {
		t.Run(name, func(t *testing.T) {
			cwd := map[string]string{"mvn-test-fail": "/work/mvn-fail", "mvn-compile-fail": "/work/mvn-compile",
				"mvn-dep-fail": "/work/mvn-dep", "mvn-dep-transfer": "/work/mvn-dep2", "mvn-multi-fail": "/work/mvn-multi"}[name]
			raw, got, err := render(t, name, cwd, "mvn", "test")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "BUILD FAILURE") {
				t.Fatal("BUILD FAILURE lost")
			}
			for _, l := range strings.Split(raw, "\n") {
				if !strings.HasPrefix(l, "[ERROR] ") {
					continue
				}
				body := strings.TrimSpace(strings.TrimPrefix(l, "[ERROR] "))
				if body == "" || strings.Contains(body, "[Help") || strings.HasPrefix(body, "To see") ||
					strings.HasPrefix(body, "Re-run") || strings.HasPrefix(body, "For more") ||
					strings.HasPrefix(body, "See dump") || strings.HasPrefix(body, "After correcting") ||
					strings.HasPrefix(body, "Failures:") || strings.HasPrefix(body, "Errors:") ||
					strings.HasPrefix(body, "Tests run:") || strings.HasPrefix(body, "BUILD") ||
					strings.Contains(body, "<<<") || strings.Contains(body, ". . .") ||
					strings.Contains(body, " ... ") || strings.HasPrefix(body, "COMPILATION") {
					continue
				}
				// Errors repeated verbatim inside "Failed to execute goal"
				// or the surefire summary are covered by their first copy;
				// compare on the location-free message tail.
				key := body
				if m := summaryEntryRE.FindStringSubmatch(l); m != nil {
					// Surefire's own summary entry: covered by the FAIL/ERROR block.
					key = "." + m[2]
				}
				if i := strings.LastIndex(key, "/work/"); i >= 0 {
					key = key[i+len("/work/"):]
					key = key[strings.Index(key, "/")+1:]
				}
				if !strings.Contains(got, key) && !strings.Contains(got, strings.SplitN(key, " ", 2)[0]) {
					t.Errorf("error line lost: %q\n%s", l, got)
				}
			}
		})
	}
}

func TestGolden(t *testing.T) {
	for name, cwd := range map[string]string{"mvn-test-fail": "/work/mvn-fail", "mvn-compile-fail": "/work/mvn-compile", "mvn-multi-fail": "/work/mvn-multi"} {
		t.Run(name, func(t *testing.T) {
			_, got, err := render(t, name, cwd, "mvn", "test")
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
				t.Errorf("golden mismatch for %s; run with -update\n--- got\n%s", name, got)
			}
		})
	}
}

func TestInapplicable(t *testing.T) {
	f := newFormatter("mvn")
	cases := []struct {
		name  string
		argv  []string
		input string
	}{
		{"debug flag", []string{"mvn", "-X", "test"}, fixture(t, "mvn-test-fail")},
		{"long debug flag", []string{"mvn", "--debug", "test"}, fixture(t, "mvn-test-fail")},
		{"version", []string{"mvn", "--version"}, "Apache Maven 3.10.1\n"},
		{"no BUILD footer (quiet failure)", []string{"mvn", "-q", "test"}, "[ERROR] boom\n"},
		{"empty (quiet success)", []string{"mvn", "-q", "test"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(tc.input)})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestRelaxedDropsDownloadsAndCountsThem(t *testing.T) {
	raw := fixture(t, "mvn-test-ok-cold")
	r, err := newFormatter("mvn").Relaxed(context.Background(), format.Input{Argv: []string{"mvn", "-q", "test"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	got := string(r.Body)
	if strings.Contains(got, "Downloading from") || strings.Contains(got, "Progress (") {
		t.Errorf("download chatter survived:\n%.400s", got)
	}
	if !strings.Contains(got, "download lines elided") || !r.Elided {
		t.Error("elision not marked")
	}
	if !strings.Contains(got, "BUILD SUCCESS") || !strings.Contains(got, "Tests run: 2") {
		t.Error("relaxed tier lost build result")
	}
	if len(got) >= len(raw)/5 {
		t.Errorf("relaxed render %d not much smaller than %d", len(got), len(raw))
	}
}

func TestRelaxedDeclinesWhenNothingToDo(t *testing.T) {
	_, err := newFormatter("mvn").Relaxed(context.Background(), format.Input{Argv: []string{"mvn", "-q"}, Stdout: strings.NewReader("[ERROR] boom\n")})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err = %v", err)
	}
}

func TestDescriptors(t *testing.T) {
	var got []string
	for _, f := range All() {
		got = append(got, f.Descriptor().Command)
	}
	if strings.Join(got, ",") != "mvn,mvnw,mvnd" {
		t.Fatalf("commands = %v", got)
	}
}
