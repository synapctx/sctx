package run

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

type decliningFormatter struct{}

func (decliningFormatter) Descriptor() format.Match { return format.Match{Command: "go"} }
func (decliningFormatter) Aggressive(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
func (decliningFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// TestDeliberateDeclineIsRecordedAsSuch — the degradation log is the coverage meter
// that decides what to build next. Before this, a formatter deliberately bypassing
// itself (`go test -list`, so its answer survives) and a formatter silently producing
// nothing both arrived at verbatim with an empty anomaly, so working behaviour was
// listed as a problem to investigate.
func TestDeliberateDeclineIsRecordedAsSuch(t *testing.T) {
	in := format.Input{Argv: []string{"go", "test", "-list", "X"}, Command: "go test",
		Stdout: strings.NewReader("TestA\nTestB\n")}

	got := renderChain(context.Background(), decliningFormatter{}, in, []byte("TestA\nTestB\n"), nil, "", nil)

	if got.Tier != format.TierVerbatim {
		t.Fatalf("a fully-declining formatter must fall through to verbatim, got %s", got.Tier)
	}
	if got.Anomaly != DeclinedMarker {
		t.Errorf("anomaly = %q, want the declined marker so the log can tell a bypass from a fault", got.Anomaly)
	}
}
