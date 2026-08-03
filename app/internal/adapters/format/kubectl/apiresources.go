package kubectl

import "github.com/synapctx/sctx/internal/domain/format"

// maxAPIResourcesRows caps the number of rows kept from `kubectl
// api-resources`, which can otherwise list 60-100+ resource types.
const maxAPIResourcesRows = 40

// aggressiveAPIResources caps the api-resources table to its leading rows.
func aggressiveAPIResources(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := renderCappedTable(lines[0], lines[1:], maxAPIResourcesRows)
	return format.Rendered{Body: body}, nil
}
