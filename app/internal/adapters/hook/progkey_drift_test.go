package hook

import (
	"testing"

	"github.com/synapctx/sctx/internal/platform/progkey"
)

// TestSubcommandTableIsCoveredByProgkey keeps two sets in correspondence without making
// either a copy of the other.
//
// subcommandTable answers "which subcommands do we REWRITE"; progkey answers "does this
// program's first argument name an operation". They are different questions, but every
// program that declares subcommands here must be subcommand-bearing there — otherwise
// adding a rewrite-table entry silently downgrades its telemetry to the bare program name
// and the coverage-gap meter stops distinguishing, say, `terraform plan` from
// `terraform apply`.
//
// The reverse containment is deliberately NOT asserted: progkey knows about programs sctx
// does not rewrite yet, which is exactly what the coverage-gap meter is for.
func TestSubcommandTableIsCoveredByProgkey(t *testing.T) {
	for program, subs := range subcommandTable {
		if subs == nil {
			// nil means "no subcommand concept" — ls, find, cat, grep, make. These MUST
			// NOT be subcommand-bearing, or their first argument (a path, pattern or
			// target) rejoins the telemetry key and the leak returns.
			if progkey.HasSubcommands(program) {
				t.Errorf("subcommandTable[%q] is nil (takes arguments, not subcommands) but progkey treats it as subcommand-bearing: its paths/patterns would leak into telemetry", program)
			}
			continue
		}
		if !progkey.HasSubcommands(program) {
			t.Errorf("subcommandTable[%q] declares subcommands %v but progkey does not treat it as subcommand-bearing: telemetry would record only %q and the meter could not tell its subcommands apart", program, subs, program)
		}
	}
}
