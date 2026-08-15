package golangcilint

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxedMarksThreeLineRunAndPreservesVerboseInfo(t *testing.T) {
	line := "pkg/a/a.go:1:1: unused variable x (unused)"
	stdout := "level=info msg=\"[runner] processed files\"\n" +
		line + "\n" + line + "\n" + line + "\n"
	out, err := New().Relaxed(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run", "-v"},
		Stdout: strings.NewReader(stdout),
	})
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "level=info") {
		t.Errorf("explicit verbose information was lost: %q", body)
	}
	if !strings.Contains(body, line+" ×3") {
		t.Errorf("duplicate run lacks exact marker: %q", body)
	}
}

func TestRelaxedDoesNotSilentlyCollapseTwoLines(t *testing.T) {
	line := "pkg/a/a.go:1:1: unused variable x (unused)\n"
	in := format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader(line + line),
	}
	if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedFoldsMarkedStderrRun(t *testing.T) {
	out, err := New().Relaxed(context.Background(), format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stderr: strings.NewReader("retry\nretry\nretry\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || string(out.Body) != "retry ×3" {
		t.Fatalf("FoldStderr=%v body=%q", out.FoldStderr, out.Body)
	}
}

func TestRelaxedNeverTouchesMachineReadableStdout(t *testing.T) {
	in := format.Input{
		Argv:   []string{"golangci-lint", "run", "--output.sarif.path=stdout"},
		Stdout: strings.NewReader("}\n}\n}\n"),
	}
	if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedNeverFoldsMachineReadableStderr(t *testing.T) {
	in := format.Input{
		Argv:   []string{"golangci-lint", "run", "--output.json.path=stderr"},
		Stdout: strings.NewReader("pkg/a.go:1:1: finding (unused)\n"),
		Stderr: strings.NewReader("}\n}\n}\n"),
	}
	if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedSniffsConfigSelectedMachineOutput(t *testing.T) {
	for name, output := range map[string]string{
		"json":     "{\n}\n}\n}\n",
		"xml":      "<checkstyle>\n</checkstyle>\n</checkstyle>\n</checkstyle>\n",
		"teamcity": "##teamcity[testStarted name='lint']\nretry\nretry\nretry\n",
		"tab":      "main.go:1:1\tfinding\terrcheck\nretry\nretry\nretry\n",
	} {
		t.Run(name, func(t *testing.T) {
			in := format.Input{Argv: []string{"golangci-lint", "run"}, Stdout: strings.NewReader(output)}
			if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
				t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
			}
		})
	}
}
