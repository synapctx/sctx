package dbt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) (string, int) {
	t.Helper()
	read := func(ext string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", "dbt112_"+name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatal(err)
	}
	return read("stdout"), exit
}

func render(t *testing.T, name string, argv ...string) (string, string, error) {
	t.Helper()
	raw, exit := load(t, name)
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: append([]string{"dbt"}, argv...), Stdout: strings.NewReader(raw), ExitCode: exit,
	})
	return string(out.Body), raw, err
}

func TestSuccessfulRunCollapsesPerNodeLines(t *testing.T) {
	body, raw, err := render(t, "run", "run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OK ×14 (per-node lines collapsed)", "Found 14 models, 5 data tests, 476 macros", "Finished running 1 table model, 13 view models", "Completed successfully", "Done. PASS=14 WARN=0 ERROR=0 SKIP=0 NO-OP=0 REUSED=0 TOTAL=14", "MissingArgumentsPropertyInGenericTestDeprecation"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	for _, bad := range []string{"\x1b", "START", "02:4", "stg_customers_10", "Registered adapter"} {
		if strings.Contains(body, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
	if len(body)*2 > len(raw) {
		t.Errorf("expected at least 2x reduction: %d -> %d", len(raw), len(body))
	}
}

func TestFailuresKeepModelMessageAndCompiledPath(t *testing.T) {
	for _, name := range []string{"runfail", "runfail_nc", "buildfail"} {
		t.Run(name, func(t *testing.T) {
			argv := []string{"run"}
			if name == "buildfail" {
				argv = []string{"build"}
			}
			body, _, err := render(t, name, argv...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"ERROR creating sql table model dbt_dev.broken", "SKIP relation dbt_dev.downstream",
				"Completed with", "in model broken (models/marts/broken.sql)",
				`relation "public.nosuch_tbl" does not exist`, "compiled code at target/run/shop/models/marts/broken.sql",
				"compiled code at target/compiled/shop/models/marts/broken.sql", "ERROR=",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q in\n%s", want, body)
				}
			}
		})
	}
}

func TestFailedTestsKeepFailLineAndSummary(t *testing.T) {
	body, _, err := render(t, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PASS ×4", "FAIL 2 accepted_values_stg_orders_status__paid__pending", "Got 2 results, configured to fail if != 0", "compiled code at target/compiled/shop/models/staging/schema.yml/accepted_values_stg_orders_status__paid__pending.sql", "Done. PASS=4 WARN=0 ERROR=1"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "unique_stg_orders_id") {
		t.Error("passing test names should be counted, not listed")
	}
}

func TestOtherSubcommandsOnlyLoseColourAndTimestamps(t *testing.T) {
	for _, tc := range []struct{ name, sub, want string }{
		{"compfail", "compile", "Model 'model.shop.badref' (models/marts/badref.sql) depends on a node named 'missing_model' which was not found"},
		{"debug", "debug", "Error from git --help: Could not find command"},
		{"deps", "deps", "Warning: No packages were found in packages.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _, err := render(t, tc.name, tc.sub)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, tc.want) || strings.Contains(body, "\x1b") {
				t.Errorf("body = %q", body)
			}
		})
	}
}

func TestSkipCascadeIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("02:41:19  Found 30 models\n")
	for i := 1; i <= 25; i++ {
		b.WriteString("02:41:20  " + strconv.Itoa(i) + " of 25 SKIP relation s.m" + strconv.Itoa(i) + " ........ [SKIP]\n")
	}
	b.WriteString("02:41:21  Done. PASS=0 WARN=0 ERROR=1 SKIP=25 NO-OP=0 REUSED=0 TOTAL=26\n")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"dbt", "run"}, Stdout: strings.NewReader(b.String()), ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if strings.Count(body, "SKIP relation") != skipCap || !strings.Contains(body, "…+15 more SKIP") || !out.Elided || !strings.Contains(body, "SKIP=25") {
		t.Errorf("body = %s", body)
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := load(t, "run")
	for _, tc := range []struct {
		name string
		argv []string
		in   string
	}{
		{"ls is machine output", []string{"dbt", "ls"}, raw},
		{"json log", []string{"dbt", "--log-format", "json", "run"}, raw},
		{"json lines", []string{"dbt", "run"}, "{\"info\":{\"ts\":1}}\n{\"info\":{\"ts\":2}}\n"},
		{"unknown", []string{"dbt", "docs", "serve"}, raw},
		{"nothing to gain", []string{"dbt", "run"}, "Done. PASS=1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New().Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(tc.in)}); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSubcommandSkipsGlobalFlags(t *testing.T) {
	for argv, want := range map[string]string{
		"dbt --no-use-colors run":                   "run",
		"dbt --profiles-dir ci --target prod build": "build",
		"dbt -t prod test --select x":               "test",
		"dbt":                                       "",
	} {
		if got := Subcommand(strings.Fields(argv)); got != want {
			t.Errorf("Subcommand(%q) = %q, want %q", argv, got, want)
		}
	}
}
