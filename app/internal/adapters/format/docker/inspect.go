package docker

import (
	"bytes"
	"context"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveInspect delegates `docker inspect`'s JSON array output to the
// generic JSON compactor. A custom `-f`/`--format` template (not JSON) fails
// jsoncompact's validity check and correctly degrades to the next tier.
func aggressiveInspect(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	jc := jsoncompact.New()
	return jc.Aggressive(ctx, format.Input{Stdout: bytes.NewReader(raw)})
}
