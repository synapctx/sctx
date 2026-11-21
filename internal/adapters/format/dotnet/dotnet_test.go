package dotnet

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
	f := New().(*Formatter)
	f.getwd = func() string { return cwd }
	r, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)})
	return raw, string(r.Body), err
}

func TestAggressiveFixtures(t *testing.T) {
	tests := []struct {
		fixture, cwd, sub string
		want, notWant     []string
		maxRatio          float64
	}{
		{"dn-restore-cold", "/work/dn-ok", "restore", []string{"restored 2 project(s)"}, []string{"Determining"}, 0.5},
		{"dn-build-ok-cold", "/work/dn-ok", "build", []string{"Lib -> Lib/bin/Debug/net11.0/Lib.dll", "Build succeeded. 0 warnings, 0 errors (00:00:01.64)"}, []string{"up-to-date for restore", "/work/"}, 0.7},
		{"dn-build-ok-warm", "/work/dn-ok", "build", []string{"Build succeeded."}, nil, 0.7},
		{"dn-test-ok", "/work/dn-ok", "test", []string{"Passed! Failed: 0, Passed: 3, Skipped: 0, Total: 3", "Lib.Tests.dll (net11.0)"}, []string{"Test run for", "A total of"}, 0.6},
		{"dn-test-fail", "/work/dn-fail", "test", []string{
			"Failed Lib.Tests.CalcTests.Positive(x: -2)", "x must be positive", "at Lib.Tests.CalcTests.Positive(Int32 x) in Lib.Tests/UnitTest1.cs:line 6",
			"Failed Lib.Tests.CalcTests.Adds", "Assert.Equal() Failure: Values differ", "Expected: 6", "Actual:   5", "UnitTest1.cs:line 4",
			"Failed Lib.Tests.CalcTests.Strs", `Expected: "hello world"`, `Actual:   "hello there"`,
			"Failed Lib.Tests.CalcTests.Divs", "System.DivideByZeroException : Attempted to divide by zero.", "at Lib.Calc.Div(Int32 a, Int32 b) in Lib/Class1.cs:line 5",
			"Skipped Lib.Tests.CalcTests.Skipped", "Failed! Failed: 4, Passed: 2, Skipped: 1, Total: 7", "framework frames"},
			[]string{"[xUnit.net", "MethodBaseInvoker", "InvokeStub"}, 0.5},
		{"dn-test-fail-nobuild", "/work/dn-fail", "test", []string{"Failed! Failed: 4"}, []string{"MethodBaseInvoker"}, 0.5},
		{"dn-build-compile-fail", "/work/dn-compile", "build", []string{
			"Lib/Class1.cs(4,41): error CS0103: The name 'c' does not exist in the current context",
			"Lib/Class1.cs(5,40): error CS0029: Cannot implicitly convert type 'int' to 'string'",
			"Lib/Class1.cs(6,16): error CS0103: The name 'Undefined' does not exist in the current context",
			"Build FAILED. 0 warnings, 3 errors"}, []string{".csproj]"}, 0.45},
		{"dn-build-warn", "/work/dn-warn", "build", []string{
			"Lib.Tests/UnitTest1.cs(4,54): error CS0246", "warning CS8618 ×5", "Lib/Class1.cs(13,19)", "…+2 more",
			"Lib/Class1.cs(7,42): warning CS8602", "Lib/Class1.cs(9,29): warning CS0219", "Lib/Class1.cs(8,9): warning CS0169",
			"Build FAILED. 8 warnings, 1 errors"}, []string{".csproj]"}, 0.45},
		{"dn-restore-fail", "/work/dn-dep", "restore", []string{"Lib.Tests/Lib.Tests.csproj: error NU1101: Unable to find package Nonexistent.Package.Xyz"}, nil, 0.9},
		{"dn-build-dep-fail", "/work/dn-dep", "build", []string{"error NU1101: Unable to find package Nonexistent.Package.Xyz", "Build FAILED. 0 warnings, 1 errors"}, nil, 0.7},
		{"dn-publish", "/work/dn-ok", "publish", []string{"Lib -> Lib/bin/Release/net11.0/publish/", "Lib -> Lib/bin/Release/net11.0/Lib.dll"}, nil, 0.7},
		{"dn-format-verify", "/work/dn-fmt", "format", []string{"error WHITESPACE ×9", "Lib/Class1.cs(2,1)", "…+4 more"}, []string{".csproj]"}, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			raw, got, err := render(t, tc.fixture, tc.cwd, "dotnet", tc.sub)
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
		})
	}
}

// Every distinct error line of a failed run survives, whatever it is grouped as.
func TestFailuresNeverLoseErrors(t *testing.T) {
	cwds := map[string]string{"dn-build-compile-fail": "/work/dn-compile", "dn-build-warn": "/work/dn-warn", "dn-build-dep-fail": "/work/dn-dep", "dn-restore-fail": "/work/dn-dep"}
	for name, cwd := range cwds {
		t.Run(name, func(t *testing.T) {
			raw, got, err := render(t, name, cwd, "dotnet", "build")
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range strings.Split(raw, "\n") {
				if !strings.Contains(l, ": error ") {
					continue
				}
				want := strings.ReplaceAll(strings.ReplaceAll(l, cwd+"/", ""), " : error", ": error")
				if i := strings.LastIndex(want, " ["); i > 0 {
					want = want[:i]
				}
				if !strings.Contains(got, want) {
					t.Errorf("error lost: %q\n%s", want, got)
				}
			}
		})
	}
}

func TestGolden(t *testing.T) {
	for name, cwd := range map[string]string{"dn-test-fail": "/work/dn-fail", "dn-build-warn": "/work/dn-warn", "dn-build-compile-fail": "/work/dn-compile"} {
		t.Run(name, func(t *testing.T) {
			_, got, err := render(t, name, cwd, "dotnet", "test")
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
	f := New()
	for _, tc := range []struct {
		name  string
		argv  []string
		input string
	}{
		{"diagnostic verbosity", []string{"dotnet", "build", "-v:diag"}, fixture(t, "dn-build-compile-fail")},
		{"verbosity flag value", []string{"dotnet", "build", "-v", "d"}, fixture(t, "dn-build-compile-fail")},
		{"terminal logger", []string{"dotnet", "build", "-tl"}, fixture(t, "dn-build-compile-fail")},
		{"msbuild getProperty", []string{"dotnet", "build", "-getProperty:OutputPath"}, "{\"Properties\":{}}\n"},
		{"version", []string{"dotnet", "--version"}, "11.0.100\n"},
		{"unrecognised text", []string{"dotnet", "new", "list"}, "Template Name  Short Name\n"},
		{"empty", []string{"dotnet", "build"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(tc.input)})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRelaxed(t *testing.T) {
	raw := fixture(t, "dn-build-ok-cold")
	r, err := New().Relaxed(context.Background(), format.Input{Argv: []string{"dotnet", "build", "-v:q"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	got := string(r.Body)
	if strings.Contains(got, "Determining projects") || !strings.Contains(got, "…+2 restore lines elided") ||
		!strings.Contains(got, "Build succeeded.") || !r.Elided {
		t.Errorf("unexpected relaxed render:\n%s", got)
	}
	if _, err := New().Relaxed(context.Background(), format.Input{Argv: []string{"dotnet", "build"}, Stdout: strings.NewReader("hello\n")}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("err = %v, want inapplicable", err)
	}
}

func TestDescriptor(t *testing.T) {
	if got := New().Descriptor().Command; got != "dotnet" {
		t.Fatalf("Command = %q", got)
	}
}
