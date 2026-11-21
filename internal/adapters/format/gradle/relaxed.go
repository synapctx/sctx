package gradle

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

func relaxed(stdout, stderr []byte) (format.Rendered, error) {
	var kept []string
	dropped := 0
	for _, l := range splitLines(stdout) {
		if progressRE.MatchString(l) || strings.HasPrefix(l, "Download http") || strings.HasPrefix(l, "Downloading https://services.gradle.org") || strings.HasPrefix(l, "Fetching distribution") {
			dropped++
			continue
		}
		kept = append(kept, l)
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
	return format.Rendered{Body: []byte(strings.Join(kept, "\n")), FoldStderr: len(stderr) > 0, Elided: dropped > 0}, nil
}
