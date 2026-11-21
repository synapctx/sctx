package prisma

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, argv []string, file string, exit int) (format.Rendered, int, error) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: exit,
	})
	return got, len(raw), err
}

type sample struct {
	file   string
	exit   int
	want   []string
	absent []string
}

func check(t *testing.T, argv []string, tests []sample, mustShrink bool) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got, raw, err := run(t, argv, tc.file, tc.exit)
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
			if mustShrink && len(got.Body) >= raw {
				t.Errorf("not smaller: %d >= %d", len(got.Body), raw)
			}
			if strings.Contains(body, "…+") != got.Elided {
				t.Errorf("Elided=%v disagrees with markers in:\n%s", got.Elided, body)
			}
		})
	}
}

func declines(t *testing.T, argv []string, out string, exit int) {
	t.Helper()
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: strings.NewReader(out), Stderr: strings.NewReader(""), ExitCode: exit,
	})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("want ErrTierInapplicable, got %v", err)
	}
}

func TestRealSamples(t *testing.T) {
	check(t, []string{"prisma", "x"}, []sample{
		{"generate.txt", 0, []string{"✔ Generated Prisma Client (7.10.1)"}, []string{"Loaded Prisma config", "schema loaded"}},
		{"validate-ok.txt", 0, []string{"is valid"}, []string{"Loaded"}},
		{"migrate-dev.txt", 0, []string{"Applying migration `20261121020602_init`", "migration.sql", "Your database is now in sync", "update available: 7.10.1 -> 8.0.0-rc.34"}, []string{"┌", "pris.ly"}},
		{"migrate-status.txt", 0, []string{"Database schema is up to date!", "1 migration found"}, nil},
		{"db-push.txt", 0, []string{"already in sync"}, nil},
		{"deploy.txt", 0, []string{"No pending migrations to apply."}, nil},
	}, true)
	check(t, []string{"prisma", "validate"}, []sample{
		{"validate-bad.txt", 1, []string{"Error code: P1012", `Type "Strin" is neither a built-in type`, "-->  prisma/schema.prisma:21", "Validation Error Count: 1", "…+"}, []string{"\x1b", "20 |"}},
		{"generate-bad.txt", 1, []string{"Error code: P1012", "prisma/schema.prisma:21"}, nil},
	}, true)
}

func TestDeclines(t *testing.T) {
	declines(t, []string{"prisma", "studio"}, "Prisma Studio is up on http://localhost:5555\nPrisma schema loaded from x\n", 0)
	declines(t, []string{"prisma", "--help"}, "Usage\n", 0)
	declines(t, []string{"prisma", "generate"}, "something else entirely\n", 0)
}

// The error text of a failing command must never be lost.
func TestNonZeroExitKeepsErrors(t *testing.T) {
	got, _, err := run(t, []string{"prisma", "validate"}, "validate-bad.txt", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Body), "Error: Prisma schema validation") {
		t.Fatalf("lost error header: %s", got.Body)
	}
}
