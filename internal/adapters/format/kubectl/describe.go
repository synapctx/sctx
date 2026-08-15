package kubectl

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveDescribe preserves every field and section from kubectl's native
// description, collapsing only consecutive identical lines. Resource-specific
// describe layouts are not a stable schema: dropping an unfamiliar indented
// field can hide the exact image, condition, mount, selector, or probe needed
// to diagnose a workload.
func aggressiveDescribe(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines, changed := collapse.Runs(collapse.SplitLines(raw))
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(lines, "\n"))}, nil
}
