package trivy

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixtures: trivy 0.67.2 (linux, docker) scanning alpine:3.10, python:3.7-slim
// and a small fs project; stdout and stderr captured separately.

func load(t *testing.T, name string, argv ...string) (format.Input, int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(rd("exit")))
	o, e := rd("stdout"), rd("stderr")
	return format.Input{Argv: append([]string{"trivy"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"image", []string{"image", "alpine:3.10"}},
		{"image-crit", []string{"image", "--severity", "CRITICAL,HIGH", "python:3.7-slim"}},
		{"fs", []string{"fs", "."}},
		{"fs-fail", []string{"fs", "--exit-code", "1", "py"}},
		{"clean", []string{"fs", "k8s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, rawLen := load(t, tc.name, tc.argv...)
			got, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(golden, got.Body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, _ := os.ReadFile(golden)
			if string(got.Body) != string(want) {
				t.Fatalf("golden mismatch:\n%s", got.Body)
			}
			if len(got.Body) >= rawLen {
				t.Fatalf("not smaller: %d >= %d", len(got.Body), rawLen)
			}
			if !got.FoldStderr || !got.Elided {
				t.Fatalf("FoldStderr/Elided must be set: %+v", got)
			}
			if strings.ContainsAny(string(got.Body), "│┌└├\x1b\r") {
				t.Fatal("table borders / ANSI / CR leaked")
			}
		})
	}
}

var totalHighRE = regexp.MustCompile(`(?:Total|Failures): \d+ \(.*HIGH: (\d+), CRITICAL: (\d+)\)`)

// Every CRITICAL/HIGH vulnerability the report totals claim must be listed.
func TestEveryCriticalAndHighKept(t *testing.T) {
	for _, name := range []string{"image", "image-crit", "fs", "fs-fail"} {
		in, _ := load(t, name, "image", "x")
		raw, _ := os.ReadFile(filepath.Join("testdata", name+".stdout"))
		want := 0
		for _, m := range totalHighRE.FindAllStringSubmatch(string(raw), -1) {
			h, _ := strconv.Atoi(m[1])
			c, _ := strconv.Atoi(m[2])
			want += h + c
		}
		got, err := New().Aggressive(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		have := len(regexp.MustCompile(`(?m)^(?:    \S+ | {2})(HIGH|CRITICAL)\b`).FindAllString(string(got.Body), -1))
		if have != want {
			t.Errorf("%s: kept %d CRITICAL/HIGH, report totals say %d", name, have, want)
		}
	}
}

func TestKeepsDetails(t *testing.T) {
	in, _ := load(t, "image", "image", "alpine:3.10")
	got, _ := New().Aggressive(context.Background(), in)
	for _, w := range []string{"apk-tools 2.10.6-r0", "CVE-2021-36159 CRITICAL -> 2.10.7-r0",
		"WARN This OS version is no longer supported", "Total: 1 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 0, CRITICAL: 1)"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q in:\n%s", w, got.Body)
		}
	}
	if !strings.Contains(string(got.Body), "…+") {
		t.Error("no elision marker")
	}
}

func TestMergedCellsInherit(t *testing.T) {
	in, _ := load(t, "fs", "fs", ".")
	got, _ := New().Aggressive(context.Background(), in)
	// django rows 2..n have blank Library/Severity/Installed cells in the raw table.
	for _, w := range []string{"django 2.2.0", "CVE-2019-19844 CRITICAL -> 1.11.27, 2.2.9, 3.0.1"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestSecretsNeverCarryValues(t *testing.T) {
	in, _ := load(t, "fs", "fs", ".")
	got, _ := New().Aggressive(context.Background(), in)
	if !strings.Contains(string(got.Body), "CRITICAL secret GitHub") && !strings.Contains(string(got.Body), "secret GitHub") {
		t.Errorf("secret finding lost:\n%s", got.Body)
	}
	if strings.Contains(string(got.Body), "xxxxxxxxxxxxxxxx") || strings.Contains(string(got.Body), "****") {
		t.Error("secret excerpt emitted")
	}
}

func TestFatalErrorSurvives(t *testing.T) {
	in, _ := load(t, "missing", "image", "nosuchimage:zz")
	// stdout holds only the empty summary; the FATAL line is the signal.
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		return // declining hands the raw bytes to the user: also safe
	}
	if !strings.Contains(string(got.Body), "FATAL") {
		t.Fatalf("FATAL error lost:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"image", "-f", "json", "x"}, {"image", "--format=sarif", "x"}, {"fs", "--format", "cyclonedx", "."},
		{"image", "-o", "out.txt", "x"}, {"image", "--format", "template", "--template", "@x", "y"},
	} {
		in, _ := load(t, "image", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "Version: 0.67.2\n"} {
		in := format.Input{Argv: []string{"trivy", "version"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "image", "image", "x")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
