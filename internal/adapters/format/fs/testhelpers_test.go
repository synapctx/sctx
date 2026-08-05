package fs

import (
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

func stdoutInput(command string, raw string) format.Input {
	return format.Input{
		Command: command,
		Stdout:  strings.NewReader(raw),
	}
}

var testCtx = context.Background()
