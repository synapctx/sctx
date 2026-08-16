package read

import (
	"github.com/synapctx/sctx/internal/adapters/format/jsonlines"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Keep these aliases local because the read formatter's tests assert the public
// rendering contract without duplicating the shared policy values.
const (
	keepJSONLLines = jsonlines.KeepRecords
	tailJSONLLines = jsonlines.TailRecords
)

func renderJSONL(raw []byte) (format.Rendered, bool) {
	return jsonlines.Render(raw)
}
