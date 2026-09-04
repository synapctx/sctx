package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/projectfilter"
)

func TestRunFiltersVerifyAndExplicitTrust(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".sctx"), 0o700); err != nil {
		t.Fatal(err)
	}
	stdout := "start\\n" + strings.Repeat("noise\\n", 20) + "done\\n"
	config := `{"version":1,"filters":[{"id":"check","command":"scripts/check","finite":true,"drop_exact_lines":["noise"],"fixtures":[{"name":"captured","stdout":"` + stdout + `","applied":true,"expected_stdout":"start\ndone\n…+20 lines filtered by project rule check"}]}]}`
	if err := os.WriteFile(filepath.Join(root, ".sctx", "filters.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	if code := runFilters([]string{"verify"}); code != 0 {
		t.Fatalf("verify exit = %d", code)
	}
	if code := runFilters([]string{"trust"}); code != 2 {
		t.Fatalf("trust without --yes exit = %d", code)
	}
	if code := runFilters([]string{"trust", "--yes"}); code != 0 {
		t.Fatalf("trust exit = %d", code)
	}
	trustPath, err := projectfilter.DefaultTrustPath()
	if err != nil {
		t.Fatal(err)
	}
	loaded, found, err := projectfilter.LoadTrustedFrom(root, trustPath)
	canonicalRoot, canonicalErr := filepath.EvalSymlinks(root)
	if err != nil || canonicalErr != nil || !found || loaded.Root != canonicalRoot {
		t.Fatalf("trusted load = %+v, found=%t, err=%v", loaded, found, err)
	}
}

func TestRunFiltersRejectsUnknownCommandBeforeDiscovery(t *testing.T) {
	if code := runFilters([]string{"unknown"}); code != 2 {
		t.Fatalf("unknown exit = %d", code)
	}
}
