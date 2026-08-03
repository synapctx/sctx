package main

import (
	"strings"
	"testing"
)

// No build tag: the privacy claim must hold in BOTH builds, because the tagless
// one still prints this text when a user asks `sctx watch --help`.

func TestThePostureNamesWhatIsAndIsNotSent(t *testing.T) {
	// The privacy claim is only worth as much as the developer's ability to
	// check it, so it is printed at the point of use rather than living only in
	// documentation. If this text ever stops saying what is withheld, the
	// command has started asking for trust it does not explain.
	var sb strings.Builder
	usage := watchUsageText()
	sb.WriteString(usage)

	for _, must := range []string{"UNCOMMITTED", "signatures", "hashes", "never bodies"} {
		if !strings.Contains(sb.String(), must) {
			t.Fatalf("watch usage does not mention %q", must)
		}
	}
}
