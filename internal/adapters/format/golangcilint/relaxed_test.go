package golangcilint

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxedDropsRuntimeNoiseAndDupes(t *testing.T) {
	f := New()
	stdout := "level=info msg=\"[runner] processed files\"\n" +
		"pkg/a/a.go:1:1: unused variable x (unused)\n" +
		"pkg/a/a.go:1:1: unused variable x (unused)\n"
	in := format.Input{
		Argv:   []string{"golangci-lint", "run"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if strings.Contains(string(out.Body), "level=info") {
		t.Errorf("runtime noise not dropped: %q", out.Body)
	}
	if strings.Count(string(out.Body), "unused variable x") != 1 {
		t.Errorf("duplicate line not collapsed: %q", out.Body)
	}
}
