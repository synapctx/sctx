package cc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, argv []string, stderr string, exit int, tier func(*Formatter, context.Context, format.Input) (format.Rendered, error)) (format.Rendered, error) {
	t.Helper()
	f := &Formatter{command: argv[0]}
	return tier(f, context.Background(), format.Input{Argv: argv, Stderr: strings.NewReader(stderr), ExitCode: exit})
}

func TestAggressiveKeepsEveryDiagnostic(t *testing.T) {
	cases := []struct {
		file    string
		argv    []string
		exit    int
		keep    []string
		dropped []string
	}{
		{"clang-warn.txt", []string{"clang", "-Wall", "warn.c"}, 1,
			[]string{"warn.c:6:26: warning: data argument not used", "warn.c:7:5: error: non-void function 'main'", "warn.c:3:9: warning: unused variable 'unused'", "4 warnings and 1 error generated.", "warnings by flag: -Wunused-variable ×2"},
			[]string{"printf(\"%d\\n\", argc, 3);", "^~~~~~"}},
		{"clang-err.txt", []string{"clang", "bad.c"}, 1,
			[]string{"error: use of undeclared identifier 'undefined_var'", "error: expected ';' at end of declaration", "5 errors generated."},
			[]string{"int x = \"str\";"}},
		{"clang-include.txt", []string{"clang", "inc.c"}, 1,
			[]string{"In file included from inc.c:1:", "error: use of undeclared identifier 'nope'"}, []string{"static inline"}},
		{"clangpp-err.txt", []string{"clang++", "t.cpp"}, 1,
			[]string{"t.cpp:3:33: error: no matching member function", "note: candidate function not viable", "4 errors generated."}, []string{"_LIBCPP_HIDE_FROM_ABI"}},
		{"clang-werror.txt", []string{"clang", "-Werror", "warn.c"}, 1,
			[]string{"[-Werror,-Wformat-extra-args]"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(t, tc.argv, raw, tc.exit, (*Formatter).Aggressive)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("render %d >= raw %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.dropped {
				if strings.Contains(body, d) {
					t.Errorf("kept excerpt %q", d)
				}
			}
			if !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
			// Every raw header line must survive.
			for _, l := range strings.Split(raw, "\n") {
				if headerRE.MatchString(l) && !strings.Contains(body, l) {
					t.Errorf("header line lost: %q", l)
				}
			}
		})
	}
}

func TestGNUDiagnostics(t *testing.T) {
	cases := []struct {
		file string
		argv []string
		keep []string
		drop []string
	}{
		{"gcc-warn.txt", []string{"gcc", "-Wall", "-Wextra", "-c", "warn.c"},
			[]string{"warn.c: In function 'helper':", "warn.c:3:41: warning: unused variable 'x' [-Wunused-variable]", "warn.c: At top level:",
				"warn.c:9:5: error: 'return' with no value", "warn.c:4:5: note: declared here", "warnings by flag: -Wunused-parameter ×2, -Wunused-variable ×2"},
			[]string{"      |              char *", "int main(int argc, char **argv) {"}},
		{"gcc-err.txt", []string{"gcc", "err.c"},
			[]string{"err.c:2:36: note: each undeclared identifier", "err.c:7:15: error: implicit declaration of function 'malloc'", "err.c:8:5: error: expected ',' or ';' before 'return'"},
			[]string{"  +++ |+#include <stdlib.h>", "    1 | #include <stdio.h>"}},
		{"gcc-include.txt", []string{"gcc", "-c", "inc.c"},
			[]string{"In file included from inc.c:1:", "inc/h.h: In function 'bad_h':", "inc/h.h:1:51: error: 'nope' undeclared"}, []string{"static inline"}},
		{"gcc-werror.txt", []string{"gcc", "-Wall", "-Werror", "-c", "warn.c"},
			[]string{"[-Werror=unused-variable]", "cc1: all warnings being treated as errors"}, []string{`printf("%d\n", argc, 3);`}},
		{"gpp-err.txt", []string{"g++", "t.cpp"},
			[]string{"t.cpp: In function 'int main()':", "In file included from /usr/local/include/c++/14.4.0/vector:66,", "                 from t.cpp:1:",
				"stl_vector.h:1296:7: note: candidate:", "t.cpp:7:5: error: 'undefined_fn' was not declared in this scope"},
			[]string{" 1296 |       push_back(const value_type& __x)", "~~~~~~~~~~~^~~"}},
	}
	for _, tc := range cases {
		for name, tier := range map[string]func(*Formatter, context.Context, format.Input) (format.Rendered, error){"aggressive": (*Formatter).Aggressive, "relaxed": (*Formatter).Relaxed} {
			t.Run(tc.file+"/"+name, func(t *testing.T) {
				raw := fixture(t, tc.file)
				out, err := run(t, tc.argv, raw, 1, tier)
				if err != nil {
					t.Fatal(err)
				}
				body := string(out.Body)
				if len(body) >= len(raw) {
					t.Errorf("render %d >= raw %d", len(body), len(raw))
				}
				for _, k := range tc.keep {
					if name == "relaxed" && strings.HasPrefix(k, "warnings by flag") {
						continue
					}
					if !strings.Contains(body, k) {
						t.Errorf("lost %q in:\n%s", k, body)
					}
				}
				for _, d := range tc.drop {
					if strings.Contains(body, d) {
						t.Errorf("kept excerpt %q", d)
					}
				}
				for _, l := range strings.Split(raw, "\n") {
					if headerRE.MatchString(l) && !strings.Contains(body, l) && name == "relaxed" {
						t.Errorf("header line lost: %q", l)
					}
				}
			})
		}
	}
}

func TestGNULinkerErrorsPassThrough(t *testing.T) {
	raw := fixture(t, "gcc-link.txt")
	if _, err := run(t, []string{"gcc", "link.c"}, raw, 1, (*Formatter).Aggressive); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("nothing to drop must decline, got %v", err)
	}
}

func TestGNUCleanBuildDeclines(t *testing.T) {
	if _, err := run(t, []string{"gcc", "ok.c"}, fixture(t, "gcc-ok.txt"), 0, (*Formatter).Aggressive); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("got %v", err)
	}
}

func TestLinkerErrorsPassThrough(t *testing.T) {
	raw := fixture(t, "clang-link.txt")
	if _, err := run(t, []string{"clang", "link.c"}, raw, 1, (*Formatter).Aggressive); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("nothing to drop must decline, got %v", err)
	}
}

func TestCleanBuildDeclines(t *testing.T) {
	for _, tier := range []func(*Formatter, context.Context, format.Input) (format.Rendered, error){(*Formatter).Aggressive, (*Formatter).Relaxed} {
		if _, err := run(t, []string{"clang", "good.c"}, fixture(t, "clang-ok.txt"), 0, tier); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("got %v", err)
		}
	}
}

func TestRelaxedKeepsDuplicates(t *testing.T) {
	raw := fixture(t, "clang-warn.txt")
	out, err := run(t, []string{"clang", "warn.c"}, raw, 1, (*Formatter).Relaxed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Body), "warnings by flag") {
		t.Error("relaxed must not add a histogram")
	}
}

func TestMachineAndPayloadFlagsDecline(t *testing.T) {
	raw := fixture(t, "clang-warn.txt")
	for _, argv := range [][]string{
		{"clang", "-E", "a.c"}, {"gcc", "-M", "a.c"}, {"clang", "-v", "a.c"}, {"cc", "--version"},
		{"clang", "-S", "-o", "-", "a.c"}, {"clang", "-fdiagnostics-format=json", "a.c"}, {"gcc", "-print-search-dirs"},
	} {
		if _, err := run(t, argv, raw, 1, (*Formatter).Aggressive); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}

func TestDedupeFoldsRepeats(t *testing.T) {
	d := "h.h:1:5: warning: unused variable 'q' [-Wunused-variable]\n    1 | int q;\n      |     ^\n"
	out, err := run(t, []string{"clang", "a.c"}, d+d+d, 0, (*Formatter).Aggressive)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "[-Wunused-variable] ×3") {
		t.Errorf("no fold marker: %s", out.Body)
	}
}

func TestANSIStripped(t *testing.T) {
	d := "\x1b[1ma.c:1:1: \x1b[31merror:\x1b[0m bad\n    1 | x\n      | ^\n"
	out, err := run(t, []string{"gcc", "a.c"}, d, 1, (*Formatter).Aggressive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Body), "\x1b") || !strings.Contains(string(out.Body), "a.c:1:1: error: bad") {
		t.Errorf("got %q", out.Body)
	}
}

func TestAllCoversDrivers(t *testing.T) {
	got := map[string]bool{}
	for _, f := range All() {
		got[f.Descriptor().Command] = true
	}
	for _, n := range []string{"clang", "clang++", "gcc", "g++", "cc", "c++"} {
		if !got[n] {
			t.Errorf("missing %s", n)
		}
	}
}
