package precommit

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, fix string, argv ...string) (format.Rendered, error) {
	return New().Aggressive(context.Background(), format.Input{
		Argv:   append([]string{"pre-commit"}, argv...),
		Stdout: strings.NewReader(read(t, fix+".stdout")), Stderr: strings.NewReader(read(t, fix+".stderr")), ExitCode: 1,
	})
}

func TestGolden(t *testing.T) {
	for _, fix := range []string{"fail", "second"} {
		t.Run(fix, func(t *testing.T) {
			got, err := run(t, fix, "run", "--all-files")
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", fix+".golden")
			if *update {
				_ = os.WriteFile(golden, got.Body, 0o644)
			}
			if want := read(t, fix+".golden"); string(got.Body) != want {
				t.Fatalf("golden mismatch:\n%s", got.Body)
			}
			if len(got.Body) >= len(read(t, fix+".stdout")) {
				t.Fatal("not smaller")
			}
		})
	}
}

// Every failed hook must keep its id, exit code and findings.
func TestFailedHooksKeepSignal(t *testing.T) {
	got, err := run(t, "fail", "run")
	if err != nil {
		t.Fatal(err)
	}
	body := string(got.Body)
	for _, w := range []string{
		"4 failed", "×3 passed", "×1 skipped",
		"FAILED trailing-whitespace [exit 1; files were modified by this hook]", "Fixing a.sh", "Fixing a.yaml",
		"FAILED end-of-file-fixer", "Fixing c.txt",
		"FAILED check-json", "Failed to json decode",
		"FAILED shellcheck-local", "SC2148", "SC2086", "1:6",
		"…+4 [INFO] lines",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in\n%s", w, body)
		}
	}
	if strings.Contains(body, "Did you mean") {
		t.Error("shellcheck fix block should have been condensed")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"install"}, {"autoupdate"}, {"--version"}, {}} {
		if _, err := run(t, "fail", argv...); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"pre-commit", "run"}, Stdout: strings.NewReader("An unexpected error has occurred\n"),
	})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("no hooks: want decline, got %v", err)
	}
	_, err = New().Aggressive(context.Background(), format.Input{
		Argv: []string{"pre-commit", "run"}, Stdout: strings.NewReader(read(t, "fail.stdout")), Stderr: strings.NewReader("Traceback"),
	})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("stderr: want decline, got %v", err)
	}
}
