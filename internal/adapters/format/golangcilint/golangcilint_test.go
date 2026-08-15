package golangcilint

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	if got := New().Descriptor().Command; got != "golangci-lint" {
		t.Errorf("Command = %q, want golangci-lint", got)
	}
}

func TestSubcommandSkipsGlobalFlagValue(t *testing.T) {
	argv := []string{"golangci-lint", "--color", "always", "run", "./..."}
	if got := subcommand(argv); got != "run" {
		t.Fatalf("subcommand() = %q, want run", got)
	}
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: argv,
		Stdout: strings.NewReader(
			"pkg/a.go:1:1: unused variable x (unused)\n",
		),
	})
	if err != nil || !strings.Contains(string(out.Body), "1 issue in 1 file") {
		t.Fatalf("Aggressive() = %q, %v", out.Body, err)
	}
}

func TestNonRunSubcommandInapplicable(t *testing.T) {
	in := format.Input{
		Argv:   []string{"golangci-lint", "cache", "status"},
		Stdout: strings.NewReader("some output\n"),
	}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
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

func TestAggressiveRunGroupsIssuesAndMarksContext(t *testing.T) {
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run", "./..."},
		Stdout: strings.NewReader(eightIssueFixture),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"pkg/a/a.go — 3 issues",
		"revive — 2",
		"L22:5 should not use dot imports …+2 context",
		"L15:3 G104: Errors unhandled",
		"8 issues in 3 files",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "L15:3 G104: Errors unhandled …") {
		t.Errorf("issue without context wrongly got a marker: %q", body)
	}
}

func TestMultipleLintersOnSameLineRemainDistinct(t *testing.T) {
	native := readFixture(t, "golangci-lint-v2.12.2-same-location.txt")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader(native),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"perfsprint — 1",
		"staticcheck — 2",
		"L6:14 string-format: fmt.Sprintf can be replaced",
		"L6:2 S1038: should use fmt.Printf",
		"L6:14 S1025: the argument is already a string",
		"3 issues in 1 file",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestNativeV2122FixturePreservesEveryIssueAndStats(t *testing.T) {
	// Captured verbatim from golangci-lint v2.12.2 with:
	// golangci-lint run ./...
	raw, err := os.ReadFile("testdata/golangci-lint-v2.12.2-default.txt")
	if err != nil {
		t.Fatal(err)
	}
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:     []string{"golangci-lint", "run", "./..."},
		Stdout:   strings.NewReader(string(raw)),
		ExitCode: 1,
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"L95:27 Error return value of `controlClient.Close` is not checked",
		"L151:23 Error return value of `resp.Body.Close` is not checked",
		"L179:23 Error return value of `resp.Body.Close` is not checked",
		"L345:9 Error return value of `io.Copy` is not checked",
		"L19:11 Error return value of `w.Write` is not checked",
		"L21:11 Error return value of `w.Write` is not checked",
		"L98:10 Error return value of `w.Write` is not checked",
		"L1060:23 Error return value of `resp.Body.Close` is not checked",
		"L192:3 QF1002: could use tagged switch on r.URL.Path",
		"L578:5 QF1001: could apply De Morgan's law",
		"L31:49 QF1008: could remove embedded field \"PublicKey\" from selector",
		"L32:55 QF1008: could remove embedded field \"PublicKey\" from selector",
		"L308:6 func signInURL is unused",
		"monitor.go — 3 issues (errcheck)",
		"jwks_test.go — 2 issues (staticcheck)",
		"13 issues in 8 files",
		"linters: errcheck=8 staticcheck=4 unused=1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("native issue or statistic %q was not represented", want)
		}
	}
	if len(out.Body) >= len(raw) {
		t.Errorf("render did not reduce native output: raw=%d rendered=%d", len(raw), len(out.Body))
	}
	rawTokens := (len(raw) + 3) / 4
	renderedTokens := (len(out.Body) + 3) / 4
	t.Logf("native fixture: %d -> %d estimated tokens (%d saved, %.1f%%)", rawTokens, renderedTokens, rawTokens-renderedTokens, 100*float64(rawTokens-renderedTokens)/float64(rawTokens))
}

func TestNativeV1648FixtureWithoutFooter(t *testing.T) {
	raw := readFixture(t, "golangci-lint-v1.64.8-default.txt")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:     []string{"golangci-lint", "run", "./..."},
		Stdout:   strings.NewReader(raw),
		ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"status.go — 3 issues (errcheck)",
		"L89:14 Error return value of `fmt.Sscanf` is not checked",
		"purpose_test.go — 1 issue (errcheck)",
		"L34:10 Error return value of `w.Write` is not checked",
		"color.go — 1 issue (unused)",
		"L41:18 func `palette.yellow` is unused",
		"5 issues in 3 files",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "linters:") {
		t.Errorf("invented a native stats footer absent from v1 output:\n%s", body)
	}
	if len(out.Body) >= len(raw) {
		t.Errorf("render did not reduce v1 native output: raw=%d rendered=%d", len(raw), len(out.Body))
	}
}

func TestNativeWarningAndIssuePreservesBoth(t *testing.T) {
	raw := readFixture(t, "golangci-lint-v2.12.2-warning-and-issue.txt")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:     []string{"golangci-lint", "run", "--new"},
		Stdout:   strings.NewReader(raw),
		ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"L6:10 Error return value of `os.Chdir` is not checked", "additional output:\nlevel=warning", "linters: errcheck=1"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestNativeBuildTaggedAndPathScopedOutput(t *testing.T) {
	raw := readFixture(t, "golangci-lint-v2.12.2-build-tags.txt")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:     []string{"golangci-lint", "run", "--build-tags", "fixturetag", "./..."},
		Stdout:   strings.NewReader(raw),
		ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"errcheck — 1", "unused — 1", "L8:10 Error return value", "L7:6 func taggedFinding is unused", "2 issues in 1 file"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestNativeCleanAndFailuresDeclineToVerbatim(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		argv     []string
		exitCode int
	}{
		{name: "clean", fixture: "golangci-lint-v2.12.2-clean.txt", argv: []string{"golangci-lint", "run"}},
		{name: "typecheck", fixture: "golangci-lint-v2.12.2-typecheck.txt", argv: []string{"golangci-lint", "run"}, exitCode: 1},
		{name: "config", fixture: "golangci-lint-v2.12.2-config-error.txt", argv: []string{"golangci-lint", "run"}, exitCode: 3},
		{name: "timeout", fixture: "golangci-lint-v2.12.2-timeout.txt", argv: []string{"golangci-lint", "run", "--timeout", "1ns"}, exitCode: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := readFixture(t, tt.fixture)
			input := func() format.Input {
				return format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: tt.exitCode}
			}
			if _, err := New().Aggressive(context.Background(), input()); err != format.ErrTierInapplicable {
				t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
			}
			if _, err := New().Relaxed(context.Background(), input()); err != format.ErrTierInapplicable {
				t.Errorf("Relaxed() error = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCustomIssuesExitCodeStillFormats(t *testing.T) {
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:     []string{"golangci-lint", "run", "--issues-exit-code", "7"},
		Stdout:   strings.NewReader("pkg/a.go:1:1: unused variable x (unused)\n"),
		ExitCode: 7,
	})
	if err != nil || !strings.Contains(string(out.Body), "1 issue in 1 file") {
		t.Fatalf("Aggressive() = %q, %v", out.Body, err)
	}
}

func TestConfigurationFailureDegradesRegardlessOfExitCode(t *testing.T) {
	in := format.Input{
		Argv:     []string{"golangci-lint", "run"},
		Stderr:   strings.NewReader("level=error msg=\"failed to load config\"\n"),
		ExitCode: 2,
	}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestNoLinterOutputWhenExplicitlyRequested(t *testing.T) {
	native := "dir with spaces/a.go:4:2: message ending in parentheses (details)\n" +
		"1 issues:\n* unused: 1\n"
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run", "--output.text.print-linter-name=false"},
		Stdout: strings.NewReader(native),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "dir with spaces/a.go — 1 issue") ||
		!strings.Contains(body, "L4:2 message ending in parentheses (details)") ||
		!strings.Contains(body, "linters: unused=1") {
		t.Fatalf("unexpected body:\n%s", body)
	}
}

func TestWarningsArePreservedAsAdditionalOutput(t *testing.T) {
	native := "level=warning msg=\"deprecated configuration option\"\n" +
		"pkg/a.go:1:1: unused variable x (unused)\n"
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader(native),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "additional output:\nlevel=warning") {
		t.Fatalf("warning lost:\n%s", out.Body)
	}
}

func TestStderrIssuesAreParsedAndFolded(t *testing.T) {
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stderr: strings.NewReader("pkg/a.go:1:1: unused variable x (unused)\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.Contains(string(out.Body), "unused variable x") {
		t.Fatalf("stderr issue not retained: FoldStderr=%v body=%q", out.FoldStderr, out.Body)
	}
}

func TestInconsistentNativeFooterDegrades(t *testing.T) {
	for _, native := range []string{
		"pkg/a.go:1:1: unused variable x (unused)\n2 issues:\n* unused: 1\n",
		"pkg/a.go:1:1: unused variable x (unused)\n1 issues:\n* unused: 2\n",
		"pkg/a.go:1:1: unused variable x (unused)\n1 issues:\n* errcheck: 1\n",
	} {
		in := format.Input{Argv: []string{"golangci-lint", "run"}, Stdout: strings.NewReader(native)}
		if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable for %q", err, native)
		}
	}
}

func TestMachineReadableCapturedStreamIsNeverRewritten(t *testing.T) {
	for _, name := range machineOutputNames {
		for _, stream := range []string{"stdout", "stderr"} {
			for _, argv := range [][]string{
				{"golangci-lint", "run", "--output." + name + ".path", stream},
				{"golangci-lint", "run", "--output." + name + ".path=" + stream},
			} {
				in := format.Input{Argv: argv, Stdout: strings.NewReader(`{"Issues":[]}`)}
				if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
					t.Errorf("%v: err = %v, want ErrTierInapplicable", argv, err)
				}
			}
		}
	}
}

func TestV1ExplicitOutputFormatIsNeverRewritten(t *testing.T) {
	for _, argv := range [][]string{
		{"golangci-lint", "run", "--out-format", "json"},
		{"golangci-lint", "run", "--out-format=checkstyle"},
		{"golangci-lint", "run", "--out-format", "code-climate:report.json,line-number"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader("{}\n{}\n{}\n")}
		if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("%v: Aggressive() error = %v, want ErrTierInapplicable", argv, err)
		}
		in.Stdout = strings.NewReader("{}\n{}\n{}\n")
		if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("%v: Relaxed() error = %v, want ErrTierInapplicable", argv, err)
		}
	}
}

func TestColouredTextParsesWithoutLeakingANSI(t *testing.T) {
	native := "\x1b[31mpkg/a.go:1:1: unused variable x (unused)\x1b[0m\n"
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "--color=always", "run"},
		Stdout: strings.NewReader(native),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Body), "\x1b[") || !strings.Contains(string(out.Body), "unused variable x") {
		t.Fatalf("unexpected coloured render: %q", out.Body)
	}
}

func TestZeroParseableIssuesInapplicable(t *testing.T) {
	in := format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader("0 issues.\n"),
	}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
