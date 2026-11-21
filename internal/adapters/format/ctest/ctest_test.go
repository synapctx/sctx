package ctest

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

func TestFailuresSurvive(t *testing.T) {
	cases := []struct {
		file string
		exit int
		keep []string
		drop []string
	}{
		{"ctest-fail-out.txt", 8,
			[]string{"2/3 Test #2: fail_one", "***Failed", "boom", "assertion failed: x == 2", "67% tests passed, 1 tests failed out of 3", "The following tests FAILED:", "2 - fail_one (Failed)", "Errors while running CTest"},
			[]string{"pass_one", "Start 2", "Test project"}},
		{"ctest-fail.txt", 8,
			[]string{"***Failed", "67% tests passed", "2 - fail_one (Failed)"}, []string{"pass_two"}},
		{"ctest-ok.txt", 0,
			[]string{"100% tests passed out of 2", "Total Test time"}, []string{"pass_one"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ctest"}, Stdout: strings.NewReader(raw), ExitCode: tc.exit})
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
			if !strings.Contains(body, "…+") {
				t.Error("elision unmarked")
			}
		})
	}
}

func TestListingDeclines(t *testing.T) {
	for _, argv := range [][]string{{"ctest", "-N"}, {"ctest", "--show-only=json-v1"}, {"ctest", "--version"}} {
		_, err := New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(fixture(t, "ctest-ok.txt"))})
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}

func TestUnknownShapeDeclines(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ctest"}, Stdout: strings.NewReader("something else\n")})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatal(err)
	}
}
