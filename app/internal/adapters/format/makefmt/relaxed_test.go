package makefmt

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxedCollapsesBlankAndDupeLines(t *testing.T) {
	f := New()
	stdout := "go build ./...\n\n\ngo build ./...\ngo build ./...\n"
	in := format.Input{
		Argv:   []string{"make", "build"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if strings.Contains(string(out.Body), "\n\n\n") {
		t.Errorf("blank runs not collapsed: %q", out.Body)
	}
}
