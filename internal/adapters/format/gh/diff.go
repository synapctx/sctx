package gh

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	gitformat "github.com/synapctx/sctx/internal/adapters/format/git"
	"github.com/synapctx/sctx/internal/domain/format"
)

const diffNameCap = 100

func aggressivePRDiff(ctx context.Context, in format.Input, args []string) (format.Rendered, error) {
	if colorAlways(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if hasArg(args, "--name-only") {
		raw := readAll(in.Stdout)
		lines := splitLines(raw)
		if len(lines) <= diffNameCap {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		body := strings.Join(lines[:diffNameCap], "\n") + fmt.Sprintf("\n…+%d more files", len(lines)-diffNameCap)
		return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d changed files", len(lines))}, nil
	}
	raw := readAll(in.Stdout)
	attempt := in
	attempt.Argv = []string{"git", "diff"}
	attempt.Command = "git diff"
	attempt.Stdout = bytes.NewReader(raw)
	return gitformat.New().Aggressive(ctx, attempt)
}

func colorAlways(args []string) bool {
	for i, arg := range args {
		if arg == "--color=always" {
			return true
		}
		if arg == "--color" && i+1 < len(args) && args[i+1] == "always" {
			return true
		}
	}
	return false
}
