package projectfilter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixtureConfig() Config {
	raw := "start\n" + strings.Repeat("building dependency\n", 20) + "done\n"
	expected := "start\ndone\n…+20 lines filtered by project rule make-lint"
	return Config{Version: 1, Filters: []Rule{{
		ID: "make-lint", Command: "make", ArgsPrefix: []string{"lint"}, Finite: true,
		OverrideBuiltin: true, DropExactLines: []string{"building dependency"},
		Fixtures: []Fixture{{Name: "native success", Stdout: raw, Applied: true, ExpectedStdout: expected}},
	}}}
}

func writeProject(t *testing.T, cfg Config) (root, path string) {
	t.Helper()
	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".sctx")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "filters.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestLoadVerifiesStrictConfigAndFixtures(t *testing.T) {
	root, path := writeProject(t, fixtureConfig())
	loaded, err := Load(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest == "" || len(loaded.Formatters()) != 1 || len(loaded.Matchers()) != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(",\n  \"unknown\": true\n}")...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field accepted: %v", err)
	}
}

func TestFormatterMatchesExactlyAndPreservesFailures(t *testing.T) {
	rule := fixtureConfig().Filters[0]
	f := &Formatter{root: "/repo", rule: rule}
	raw := rule.Fixtures[0].Stdout
	out, err := f.Aggressive(context.Background(), format.Input{
		Argv: []string{"make", "lint"}, Stdout: strings.NewReader(raw),
	})
	if err != nil || string(out.Body) != rule.Fixtures[0].ExpectedStdout || !out.Elided {
		t.Fatalf("render = %q, elided=%t, err=%v", out.Body, out.Elided, err)
	}
	for _, in := range []format.Input{
		{Argv: []string{"make", "test"}, Stdout: strings.NewReader(raw)},
		{Argv: []string{"make", "lint"}, Stdout: strings.NewReader(raw), ExitCode: 1},
	} {
		if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("unsafe invocation error = %v", err)
		}
	}
}

func TestRelativeProjectCommandCannotEscapeRoot(t *testing.T) {
	if commandMatches("/repo", "scripts/check", "/repo/scripts/check") != true {
		t.Fatal("project command did not match its absolute invocation")
	}
	if commandMatches("/repo", "scripts/check", "/other/scripts/check") {
		t.Fatal("same-basename command outside project matched")
	}
	if err := validateCommand("../check"); err == nil {
		t.Fatal("escaping command accepted")
	}
}

func TestTrustIsContentAndPathBound(t *testing.T) {
	root, path := writeProject(t, fixtureConfig())
	loaded, err := Load(root, path)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "filter-trust.json")
	if trusted, err := loaded.Trusted(trustPath); err != nil || trusted {
		t.Fatalf("unapproved trusted=%t err=%v", trusted, err)
	}
	if err := loaded.Trust(trustPath); err != nil {
		t.Fatal(err)
	}
	if trusted, err := loaded.Trusted(trustPath); err != nil || !trusted {
		t.Fatalf("approved trusted=%t err=%v", trusted, err)
	}
	info, err := os.Stat(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trust mode=%v", info.Mode().Perm())
	}

	_, path = writeProjectAt(t, root, fixtureConfig())
	changed, err := Load(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if trusted, trustErr := changed.Trusted(trustPath); trustErr != nil || trusted {
		t.Fatalf("changed content trusted=%t err=%v", trusted, trustErr)
	}
}

func writeProjectAt(t *testing.T, root string, cfg Config) (string, string) {
	t.Helper()
	path := filepath.Join(root, ".sctx", "filters.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path
}
