package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// lsofFormatter removes the column-alignment padding from `lsof` tables. It is
// lossless: lsof escapes spaces inside COMMAND, and the trailing NAME column
// is rejoined with single spaces, so every value survives.
type lsofFormatter struct{}

func (f *lsofFormatter) Descriptor() format.Match {
	return format.Match{Command: "lsof"}
}

func (f *lsofFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(raw)
	if len(lines) < 6 || !strings.HasPrefix(lines[0], "COMMAND") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// -F field output and other non-table shapes were excluded by the header
	// check; also leave explicit field-separated output alone.
	for _, a := range in.Argv[min(1, len(in.Argv)):] {
		if strings.HasPrefix(a, "-F") || a == "-t" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.Join(strings.Fields(l), " ")
	}
	body := strings.Join(out, "\n")
	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d rows", len(lines)-1)}, nil
}

func (f *lsofFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
