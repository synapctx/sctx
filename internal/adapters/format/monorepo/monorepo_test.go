package monorepo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, f *Formatter, argv []string, file string, exit int) (format.Rendered, int, error) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(raw), Stderr: bytes.NewReader(nil), ExitCode: exit,
	})
	return got, len(raw), err
}

func TestRealSamples(t *testing.T) {
	tests := []struct {
		f      *Formatter
		argv   []string
		file   string
		exit   int
		want   []string
		absent []string
	}{
		{NewTurbo(), []string{"turbo", "run", "build"}, "turbo-build.txt", 0,
			[]string{"turbo 2.11.13", "Packages in scope: @m/a, @m/b, @m/c", "✔ 3 ok:", "@m/a:build", "Tasks: 3 successful, 3 total", "Time: 1.035s", "…+9"},
			[]string{"cache miss, executing", "> mkdir", "compiling"}},
		{NewTurbo(), []string{"turbo", "run", "build"}, "turbo-build-cached.txt", 0,
			[]string{"@m/a:build [cache hit]", "Cached: 3 cached, 3 total", "FULL TURBO"}, []string{"replaying logs"}},
		{NewTurbo(), []string{"turbo", "run", "test"}, "turbo-test-fail.txt", 1,
			[]string{"✖ @m/b:test", "FAIL: expected 1 got 2", "running tests b", "npm error code 1", "Failed: @m/b#test", "ERROR run failed", "Tasks: 5 successful, 6 total"},
			[]string{"npm error path", "@m/b:test:", "running tests a"}},
		{NewNx(), []string{"nx", "run-many", "-t", "build"}, "nx-build-default.txt", 0,
			[]string{"nx: Running target build for 3 projects", "✔ 3 ok:", "Run duration: 1.6s", "Cache: 0/3 hit (0%)"}, []string{"Critical path", "Output of 3"}},
		{NewNx(), []string{"nx", "run-many", "-t", "build"}, "nx-build-default-cached.txt", 0,
			[]string{"@m/a:build [local cache]", "3 out of 3 tasks"}, nil},
		{NewNx(), []string{"nx", "run-many", "-t", "test"}, "nx-test-fail-default.txt", 1,
			[]string{"✖ @m/b:test", "FAIL expected 1 got 2", "Failed: @m/b:test", "Cache: 3/5 hit (60%)"}, []string{"npm error path", "> echo running"}},
		{NewNx(), []string{"nx", "run-many", "-t", "test", "--output-style=static"}, "nx-test-fail-static.txt", 1,
			[]string{"✖ @m/b:test", "FAIL expected 1 got 2", "✔ 5 ok:"}, []string{"compiling a", "running tests a"}},
		{NewNx(), []string{"nx", "run-many", "-t", "build"}, "nx-build.txt", 0,
			[]string{"nx: 3 tasks: 3 succeeded", "Cache: 0/3 hit (0%)"}, []string{"Re-run with"}},
		{NewNx(), []string{"nx", "run-many", "-t", "test"}, "nx-test-fail.txt", 1,
			[]string{"6 tasks: 5 succeeded, 3 cached, 1 failed", "✖ @m/b:test", "full log: /repo/.nx/cache/terminalOutputs/16092707175883092453"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got, raw, err := run(t, tc.f, tc.argv, tc.file, tc.exit)
			if err != nil {
				t.Fatal(err)
			}
			body := string(got.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("unexpected %q in:\n%s", a, body)
				}
			}
			if len(got.Body) >= raw {
				t.Errorf("not smaller: %d >= %d", len(got.Body), raw)
			}
			if strings.Contains(body, "…+") != got.Elided {
				t.Errorf("Elided=%v disagrees with markers in:\n%s", got.Elided, body)
			}
			t.Logf("%s: %d -> %d", tc.file, raw, len(got.Body))
		})
	}
}

func TestDeclines(t *testing.T) {
	turboOut, _ := os.ReadFile("testdata/turbo-build.txt")
	tests := []struct {
		name string
		f    *Formatter
		argv []string
		out  string
		exit int
	}{
		{"turbo not a run", NewTurbo(), []string{"turbo", "ls"}, "3 packages\n", 0},
		{"turbo dry run", NewTurbo(), []string{"turbo", "run", "build", "--dry=json"}, string(turboOut), 0},
		{"turbo graph", NewTurbo(), []string{"turbo", "run", "build", "--graph"}, string(turboOut), 0},
		{"turbo failure with nothing attributable", NewTurbo(), []string{"turbo", "run", "build"}, "   • turbo 2.0.0\n Tasks:    1 successful, 1 total\n", 1},
		{"nx not a run", NewNx(), []string{"nx", "show", "projects"}, "a\nb\n", 0},
		{"nx json", NewNx(), []string{"nx", "show", "--json"}, " NX   x\n", 0},
		{"nx failure with nothing attributable", NewNx(), []string{"nx", "build"}, " NX   something broke\n", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.f.Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: strings.NewReader(tc.out), Stderr: strings.NewReader(""), ExitCode: tc.exit,
			})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

// A warning printed by a task that still succeeded must survive.
func TestWarningInSucceededTaskSurvives(t *testing.T) {
	out := "   • turbo 2.0.0\n@a:build: cache miss, executing abc\n@a:build: compiling\n@a:build: warning: deprecated API used\n\n Tasks:    1 successful, 1 total\nCached:    0 cached, 1 total\n  Time:    1s \n"
	got, err := NewTurbo().Aggressive(context.Background(), format.Input{
		Argv: []string{"turbo", "build"}, Stdout: strings.NewReader(out), Stderr: strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Body), "warning: deprecated API used") {
		t.Fatalf("warning lost:\n%s", got.Body)
	}
}
