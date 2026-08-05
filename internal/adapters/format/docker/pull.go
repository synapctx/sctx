package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// layerLineRe matches a docker pull/push per-layer progress line, e.g.
// "a2abf6c4d29d: Pulling fs layer" or "a2abf6c4d29d: Pushed".
var layerLineRe = regexp.MustCompile(`^[0-9a-f]{12}: `)

// aggressivePullPush handles both `docker pull` and `docker push`: both emit
// one continuously-updated line per layer (Pulling/Waiting/Downloading/
// Extracting/Pull complete, or Pushed/Layer already exists). Those are
// collapsed to a single "…+N layers" marker; the leading repository line and
// the terminal Digest/Status/digest line are kept verbatim.
func aggressivePullPush(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var kept []string
	layerIDs := map[string]bool{}
	for _, line := range lines {
		if layerLineRe.MatchString(line) {
			id := strings.SplitN(line, ":", 2)[0]
			layerIDs[id] = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	if len(layerIDs) == 0 || len(kept) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	b.WriteString(kept[0])
	fmt.Fprintf(&b, "\n…+%d layers", len(layerIDs))
	for _, l := range kept[1:] {
		b.WriteString("\n")
		b.WriteString(l)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}
