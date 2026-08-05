package pip

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// showKeepKeys are the `pip show` fields worth keeping; the long
// Summary/Home-page/Author/License/Location fields are dropped as noise.
var showKeepKeys = map[string]bool{
	"Name":        true,
	"Version":     true,
	"Requires":    true,
	"Required-by": true,
}

// aggressiveShow keeps only Name/Version/Requires/Required-by from `pip
// show` output, which may cover multiple packages separated by blank lines.
func aggressiveShow(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var blocks [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		idx := strings.Index(l, ":")
		if idx <= 0 {
			continue
		}
		if showKeepKeys[l[:idx]] {
			cur = append(cur, l)
		}
	}
	flush()

	if len(blocks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []string
	for i, blk := range blocks {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, blk...)
	}

	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
}
