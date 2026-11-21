package maven

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxed drops download/progress chatter (counted) and collapses runs of
// identical lines. It is the tier for output without a BUILD footer: -q runs,
// plugin-only goals and non-English locales.
func relaxed(stdout, stderr []byte) (format.Rendered, error) {
	lines, _ := splitLog(stdout)
	var kept []string
	dropped := 0
	for _, l := range lines {
		if l.download {
			dropped++
			continue
		}
		kept = append(kept, l.text)
	}
	kept, runs := collapse.Runs(kept)
	if dropped == 0 && !runs {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("…+%d download lines elided", dropped))
	}
	if len(stderr) > 0 {
		kept = append(kept, strings.TrimRight(string(stderr), "\n"))
	}
	return format.Rendered{Body: joinBody(kept), FoldStderr: len(stderr) > 0, Elided: dropped > 0}, nil
}
