package cmake

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

func aggressive(t *testing.T, argv []string, raw string, exit int) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: exit})
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		argv []string
		exit int
		keep []string
		drop []string
	}{
		{"cmake-configure.txt", []string{"cmake", "-S", ".", "-B", "build"}, 0,
			[]string{"The C compiler identification is AppleClang", "Configuring done", "Build files have been written to"},
			[]string{"Detecting C compiler ABI info", "Check for working C compiler"}},
		{"cmake-build.txt", []string{"cmake", "--build", "build"}, 0,
			[]string{"[100%] Built target t_fail", "…+"}, []string{"Building C object CMakeFiles/core.dir"}},
		{"cmake-build-fail.txt", []string{"cmake", "--build", "build"}, 2,
			[]string{"b.c:1:24: error: use of undeclared identifier 'nope'", "a.c:1:19: warning: unused variable 'u'", "make[2]: *** [CMakeFiles/b.dir/src/b.c.o] Error 1", "make: *** [all] Error 2", "1 error generated."},
			[]string{"int fb(int x){return x+nope;}", "Linking C static library liba.a"}},
		{"cmake-configure-fail.txt", []string{"cmake", "-S", "bad"}, 1,
			[]string{"CMake Error at CMakeLists.txt:3 (find_package):", "Could not find a package configuration file provided by", "Configuring incomplete, errors occurred!"},
			[]string{"Detecting C compiler ABI info"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := aggressive(t, tc.argv, raw, tc.exit)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			if !out.Elided {
				t.Error("Elided not set")
			}
		})
	}
}

func TestRelaxedDoesNotFilterDiagnostics(t *testing.T) {
	raw := fixture(t, "cmake-build-fail.txt")
	out, err := New().Relaxed(context.Background(), format.Input{Argv: []string{"cmake", "--build", "b"}, Stdout: strings.NewReader(raw), ExitCode: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "int fb(int x){return x+nope;}") {
		t.Error("relaxed must keep excerpts")
	}
}

func TestNothingToCollapseDeclines(t *testing.T) {
	raw := fixture(t, "cmake-build-noop.txt")
	// A no-op build is three "Built target" lines: the last is kept, two drop,
	// the marker costs more than it saves.
	out, err := aggressive(t, []string{"cmake", "--build", "b"}, raw, 0)
	if err == nil && len(out.Body) >= len(raw) {
		t.Fatalf("non-smaller render returned: %q", out.Body)
	}
	if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatal(err)
	}
}

func TestToolModesDecline(t *testing.T) {
	raw := fixture(t, "cmake-configure.txt")
	for _, argv := range [][]string{{"cmake", "-E", "echo"}, {"cmake", "--version"}, {"cmake", "-LAH", "."}, {"cmake", "--install", "b"}, {"cmake", "-P", "x.cmake"}} {
		if _, err := aggressive(t, argv, raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}
