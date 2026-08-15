package kubectl

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
)

// maxGetWideRows caps the number of data rows kept from `kubectl get -o
// wide`.
const maxGetWideRows = 30

// healthyStatuses are STATUS values considered healthy when grouping ready
// rows in `kubectl get`.
var healthyStatuses = map[string]bool{
	"Running":   true,
	"Active":    true,
	"Bound":     true,
	"Completed": true,
}

const maxReadyNamesShown = 8

// aggressiveGet parses the default `kubectl get` table: it drops the AGE
// column, groups healthy rows (matching STATUS and a fully-ready READY
// count) into a compact summary, and renders non-healthy rows in full.
func aggressiveGet(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "No resources found") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			// `kubectl get TYPE,TYPE` emits multiple independently shaped
			// tables separated by blank lines. A single header cannot safely
			// describe every following row.
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}

	names, starts := parseHeader(lines[0])
	if colIndex(names, "TYPE") >= 0 && colIndex(names, "REASON") >= 0 && colIndex(names, "OBJECT") >= 0 && colIndex(names, "MESSAGE") >= 0 {
		in.Stdout = bytes.NewReader(raw)
		return aggressiveEvents(in)
	}
	nameIdx := colIndex(names, "NAME")
	if nameIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	ageIdx := colIndex(names, "AGE")
	readyIdx := colIndex(names, "READY")
	statusIdx := colIndex(names, "STATUS")
	namespaceIdx := colIndex(names, "NAMESPACE")

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Many resources (NetworkPolicy, Service, ConfigMap, CRDs, ...) do not
	// expose READY/STATUS. Calling every such row "not ready" is incorrect;
	// preserve their native table and merely cap very large results.
	if statusIdx < 0 {
		if len(rows) <= maxGetWideRows {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return format.Rendered{Body: renderCappedTable(lines[0], lines[1:], maxGetWideRows)}, nil
	}

	isHealthy := func(cols []string) bool {
		if statusIdx < 0 || !healthyStatuses[cols[statusIdx]] {
			return false
		}
		if readyIdx >= 0 {
			parts := strings.SplitN(cols[readyIdx], "/", 2)
			if len(parts) == 2 && parts[0] != parts[1] {
				return false
			}
		}
		return true
	}

	var healthyNames []string
	var otherLines []string
	notReady := 0

	for _, cols := range rows {
		if isHealthy(cols) {
			name := cols[nameIdx]
			if namespaceIdx >= 0 && cols[namespaceIdx] != "" {
				name = cols[namespaceIdx] + "/" + name
			}
			healthyNames = append(healthyNames, name)
			continue
		}
		notReady++
		var parts []string
		for i, v := range cols {
			if i == ageIdx {
				continue
			}
			parts = append(parts, v)
		}
		otherLines = append(otherLines, strings.Join(parts, " "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d items (%d not ready)", len(rows), notReady)

	if len(healthyNames) > 0 {
		shown := healthyNames
		more := 0
		if len(shown) > maxReadyNamesShown {
			more = len(shown) - maxReadyNamesShown
			shown = shown[:maxReadyNamesShown]
		}
		fmt.Fprintf(&b, "\n%d ready: %s", len(healthyNames), strings.Join(shown, ", "))
		if more > 0 {
			fmt.Fprintf(&b, ", …+%d more", more)
		}
	}

	if len(otherLines) > 0 {
		var header []string
		for i, name := range names {
			if i != ageIdx {
				header = append(header, name)
			}
		}
		b.WriteString("\nnot ready: ")
		b.WriteString(strings.Join(header, " | "))
		for _, l := range otherLines {
			b.WriteString("\n  ")
			b.WriteString(l)
		}
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}

// aggressiveJSON delegates a user-requested native JSON document to the
// generic JSON compactor. No output-format flag is injected.
func aggressiveJSON(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return jsoncompact.New().Aggressive(ctx, format.Input{Stdout: bytes.NewReader(raw)})
}

// aggressiveGetWide caps the row count of a `kubectl get -o wide` table.
// Unlike the default get table, wide adds resource-specific columns (NODE,
// NOMINATED NODE, ...), so rows are kept verbatim rather than re-grouped.
func aggressiveGetWide(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "No resources found") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= maxGetWideRows+1 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := renderCappedTable(lines[0], lines[1:], maxGetWideRows)
	return format.Rendered{Body: body}, nil
}
