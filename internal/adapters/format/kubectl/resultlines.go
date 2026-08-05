package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// resultVerbs are the trailing verbs kubectl emits on each per-object
// result line for apply/create/delete/patch/scale/label/annotate (e.g.
// "deployment.apps/x created").
var resultVerbs = map[string]bool{
	"created":    true,
	"configured": true,
	"unchanged":  true,
	"deleted":    true,
	"replaced":   true,
	"patched":    true,
	"scaled":     true,
	"labeled":    true,
	"annotated":  true,
	"pruned":     true,
}

// maxResultLinesPerVerb caps the number of object names listed per result
// verb before eliding the rest with a "…+N more" marker.
const maxResultLinesPerVerb = 8

// aggressiveResultLines groups the per-object result lines emitted by
// apply/create/delete/patch/scale/label/annotate by their trailing verb,
// capping the object list per verb. Lines that don't end in a recognized
// verb (errors, warnings) are kept verbatim and appended after the groups,
// preserving error signal.
func aggressiveResultLines(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var order []string
	items := make(map[string][]string)
	var other []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		verb := fields[len(fields)-1]
		if !resultVerbs[verb] {
			other = append(other, line)
			continue
		}
		obj := strings.TrimSpace(strings.TrimSuffix(trimmed, verb))
		if _, ok := items[verb]; !ok {
			order = append(order, verb)
		}
		items[verb] = append(items[verb], obj)
	}

	if len(order) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for i, verb := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		objs := items[verb]
		shown := objs
		more := 0
		if len(shown) > maxResultLinesPerVerb {
			more = len(shown) - maxResultLinesPerVerb
			shown = shown[:maxResultLinesPerVerb]
		}
		fmt.Fprintf(&b, "%d %s: %s", len(objs), verb, strings.Join(shown, ", "))
		if more > 0 {
			fmt.Fprintf(&b, ", …+%d more", more)
		}
	}
	for _, l := range other {
		b.WriteString("\n")
		b.WriteString(l)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
