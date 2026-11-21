package grype

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

// Fixtures: grype v0.100.0 (docker, linux) scanning alpine:3.10 and
// python:3.7-slim; table output including the EPSS and RISK columns.

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
	return format.Input{Argv: append([]string{"grype"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	for _, name := range []string{"alpine", "pyslim"} {
		t.Run(name, func(t *testing.T) {
			in, rawLen := load(t, name, "x")
			got, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden")
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
		})
	}
}

// Every Critical/High row of the raw table must be kept (id under its package).
func TestEveryCriticalAndHighKept(t *testing.T) {
	rowRE := regexp.MustCompile(`(?m)^(\S+)\s+\S+.*?\s(CVE-[\d-]+|GHSA-[\w-]+)\s+(Critical|High)\s`)
	for _, name := range []string{"alpine", "pyslim"} {
		in, _ := load(t, name, "x")
		raw, _ := os.ReadFile("testdata/" + name + ".stdout")
		got, err := New().Aggressive(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		want := rowRE.FindAllStringSubmatch(string(raw), -1)
		have := regexp.MustCompile(`(?m)^    \S+ (CRITICAL|HIGH)\b`).FindAllString(string(got.Body), -1)
		if len(want) == 0 || len(have) != len(want) {
			t.Errorf("%s: kept %d, raw has %d Critical/High rows", name, len(have), len(want))
		}
	}
	in, _ := load(t, "pyslim", "x")
	got, _ := New().Aggressive(context.Background(), in)
	for _, w := range []string{"libc6 2.36-9+deb12u1", "CVE-2023-4911 HIGH -> 2.36-9+deb12u3 KEV", "CVE-2026-41992 HIGH (won't fix)", "…+"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(string(got.Body), "EPSS") || strings.Contains(string(got.Body), "99th") {
		t.Error("EPSS/RISK data survived")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-o", "json", "x"}, {"--output=sarif", "x"}, {"-o", "cyclonedx", "x"}, {"db", "status"}, {"version"}, {"x", "--file", "out.txt"}} {
		in, _ := load(t, "pyslim", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"matches\":[]}", "NAME INSTALLED\nfoo 1\n"} {
		in := format.Input{Argv: []string{"grype", "x"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "pyslim", "x")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
