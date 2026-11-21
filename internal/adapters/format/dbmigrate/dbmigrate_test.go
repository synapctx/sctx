package dbmigrate

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

func load(t *testing.T, prefix, name string) (stdout, stderr string, exit int) {
	t.Helper()
	read := func(ext string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", prefix+"_"+name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatal(err)
	}
	return read("out"), read("err"), exit
}

func formatter(t *testing.T, command string) format.Formatter {
	t.Helper()
	for _, f := range All() {
		if f.Descriptor().Command == command {
			return f
		}
	}
	t.Fatalf("no formatter for %s", command)
	return nil
}

func render(t *testing.T, command, prefix, name string, argv ...string) (string, int, format.Rendered, error) {
	t.Helper()
	stdout, stderr, exit := load(t, prefix, name)
	out, err := formatter(t, command).Aggressive(context.Background(), format.Input{
		Argv: append([]string{command}, argv...), Stdout: strings.NewReader(stdout),
		Stderr: strings.NewReader(stderr), ExitCode: exit,
	})
	return string(out.Body), len(stdout) + len(stderr), out, err
}

func TestFlyway(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contains []string
		absent   []string
		elided   bool
	}{
		{"info2", []string{"Database: jdbc:postgresql://db:5432/migr (PostgreSQL 17.12)", "Schema version: 3", "Category|Version|Description|Type|Installed On|State|Undoable", "Versioned|3|add status|SQL|2026-11-21 02:38:01|Success|No"}, []string{"+----", "Flyway OSS Edition", "release notes"}, false},
		{"migrate", []string{`Migrating schema "public" to version "3 - add status"`, `Successfully applied 3 migrations to schema "public", now at version v3`}, []string{"Redgate"}, false},
		{"migrate2", []string{`Schema "public" is up to date. No migration necessary.`}, []string{"Redgate"}, false},
		{"validate_fail", []string{"ERROR: Validate failed: Migrations have failed validation", "Migration checksum mismatch for migration version 3", "-> Applied to database : 552437915", "-> Resolved locally    : 345412231", "Either revert the changes"}, []string{"Need more flexibility", "rd.gt/3AbJUZE"}, false},
		{"authfail", []string{"password authentication failed for user", "SQL State  : 28P01"}, nil, false},
		{"migrate_fail", []string{`ERROR: Migration of schema "public" to version "4 - broken" failed! Changes successfully rolled back.`, "ERROR: Failed to execute script V4__broken.sql", "SQL State  : 42P01", `Message    : ERROR: relation "nosuch" does not exist`, "Location   : /flyway/sql/V4__broken.sql", "Line       : 1", "Caused by: org.postgresql.util.PSQLException"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, raw, out, err := render(t, "flyway", "flyway11", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) >= raw {
				t.Fatalf("not smaller")
			}
			for _, w := range tc.contains {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in\n%s", w, body)
				}
			}
			for _, w := range tc.absent {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q", w)
				}
			}
			if out.Elided != tc.elided || !out.FoldStderr {
				t.Errorf("Elided=%v FoldStderr=%v", out.Elided, out.FoldStderr)
			}
		})
	}
}

func TestFlywayRepeatedErrorBlockIsMarkedNotDropped(t *testing.T) {
	body, _, _, err := render(t, "flyway", "flyway11", "migrate_fail")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(body, "SQL State  : 42P01"); n != 1 {
		t.Errorf("detail block kept %d times", n)
	}
	if !strings.Contains(body, "…+1 repeated error detail block") {
		t.Errorf("repeat not marked:\n%s", body)
	}
}

func TestLiquibase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contains []string
		absent   []string
		elided   bool
	}{
		{"status", []string{"3 changesets have not been applied to postgres@jdbc:postgresql://db:5432/lbdb", "changelog.xml::1::dev", "changelog.xml::3::dev", "Liquibase command 'status' was executed successfully."}, []string{"####", "Starting Liquibase", "Liquibase Version", "Open Source"}, false},
		{"update", []string{"Running Changeset: changelog.xml::2::dev", "Liquibase: Update has been successful. Rows affected: 3", "UPDATE SUMMARY", "Run:                          3"}, []string{"##"}, false},
		{"history", []string{"Liquibase History for jdbc:postgresql://db:5432/lbdb", "Deployment ID|Update Date|Changelog Path|Changeset Author|Changeset ID|Tag", "5228742620|11/21/26, 2:39 AM|changelog.xml|dev|2|"}, []string{"+----"}, false},
		{"validate", []string{"No validation errors found."}, []string{"Starting Liquibase"}, false},
		{"updatefail", []string{"Running Changeset: bad.xml::9::dev", `ERROR: Exception Primary Reason:  ERROR: relation "nosuch" does not exist`, "…+1 repeated exception block", "Unexpected error running Liquibase: Migration failed for changeset bad.xml::9::dev:", `[Failed SQL: (0) ALTER TABLE nosuch ADD COLUMN x int]`, "Run:                          0"}, []string{"Starting Liquibase"}, true},
		{"authfail", []string{`FATAL: password authentication failed for user "postgres"`, "Unexpected error running Liquibase: Connection could not be created"}, []string{"####"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, raw, out, err := render(t, "liquibase", "liquibase431", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) >= raw {
				t.Fatalf("not smaller")
			}
			for _, w := range tc.contains {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in\n%s", w, body)
				}
			}
			for _, w := range tc.absent {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q", w)
				}
			}
			if out.Elided != tc.elided || !out.FoldStderr {
				t.Errorf("Elided=%v FoldStderr=%v", out.Elided, out.FoldStderr)
			}
		})
	}
}

func TestLongChangesetRunIsCountedWithMarker(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		b.WriteString("Running Changeset: db/changelog.xml::" + strconv.Itoa(i) + "::dev\n")
	}
	b.WriteString("Liquibase command 'update' was executed successfully.\n")
	b.WriteString("Starting Liquibase at 02:39:03 using Java 17.0.14\nLiquibase Version: 4.31.1\n")
	out, err := formatter(t, "liquibase").Aggressive(context.Background(), format.Input{Argv: []string{"liquibase", "update"}, Stderr: strings.NewReader(b.String())})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "…+32 Running Changeset lines") || !strings.Contains(body, "::40::dev") || !strings.Contains(body, "::5::dev") || strings.Contains(body, "::6::dev") || !out.Elided {
		t.Errorf("body:\n%s", body)
	}
}

// A failing run never loses its error text: every ERROR/Message line in the raw
// capture is still present after compaction.
func TestFailuresKeepEveryDistinctErrorLine(t *testing.T) {
	for _, tc := range []struct{ cmd, prefix, name string }{
		{"flyway", "flyway11", "migrate_fail"}, {"flyway", "flyway11", "validate_fail"}, {"flyway", "flyway11", "authfail"},
		{"liquibase", "liquibase431", "updatefail"}, {"liquibase", "liquibase431", "authfail"},
	} {
		t.Run(tc.prefix+"/"+tc.name, func(t *testing.T) {
			body, _, _, err := render(t, tc.cmd, tc.prefix, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			stdout, stderr, _ := load(t, tc.prefix, tc.name)
			for _, l := range strings.Split(stdout+stderr, "\n") {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "ERROR: Exception") || strings.Contains(l, "rd.gt/") || strings.HasPrefix(l, "##") ||
					strings.HasPrefix(l, "Starting Liquibase") || strings.HasPrefix(l, "Liquibase Version") || strings.HasPrefix(l, "Liquibase Open") ||
					strings.HasPrefix(l, "For more information") || strings.HasPrefix(l, "Flyway OSS") || strings.HasPrefix(l, "See release") {
					continue
				}
				if !strings.Contains(body, l) {
					t.Errorf("line lost: %q", l)
				}
			}
		})
	}
}

func TestDeclinesMachineOutputAndEmpty(t *testing.T) {
	stdout, stderr, _ := load(t, "flyway11", "info2")
	for _, argv := range [][]string{
		{"flyway", "-outputType=json", "info"}, {"flyway", "-outputType", "json", "info"},
		{"liquibase", "--format=json", "status"},
	} {
		f := formatter(t, argv[0])
		if _, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr)}); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: err = %v", argv, err)
		}
	}
	if _, err := formatter(t, "flyway").Aggressive(context.Background(), format.Input{Argv: []string{"flyway", "info"}}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty: err = %v", err)
	}
	if _, err := formatter(t, "flyway").Relaxed(context.Background(), format.Input{}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: err = %v", err)
	}
}
