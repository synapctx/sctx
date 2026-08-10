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
			// nil means "wrap every invocation", which for ls, find, cat, grep and
			// make also means argv[1] is a path, pattern or target. Those MUST NOT be
			// subcommand-bearing, or that argument rejoins the telemetry key and the
			// leak returns.
			//
			// argvOneIsOperation is the documented exception: a program wrapped
			// unconditionally because its VERB surface is too large to enumerate,
			// while argv[1] is still the tool's own bounded vocabulary (`aws s3`,
			// `terraform plan`). Being on that list is what permits — and requires —
			// progkey to keep the distinction.
			switch {
			case argvOneIsOperation[program] && !progkey.HasSubcommands(program):
				t.Errorf("subcommandTable[%q] is nil and argvOneIsOperation says its first argument names an operation, but progkey does not: the meter cannot tell its operations apart", program)
			case !argvOneIsOperation[program] && progkey.HasSubcommands(program):
				t.Errorf("subcommandTable[%q] is nil (takes arguments, not subcommands) but progkey treats it as subcommand-bearing: its paths/patterns would leak into telemetry. If argv[1] really is an operation, say so in argvOneIsOperation and explain why", program)
			}
			continue
		}
		if !progkey.HasSubcommands(program) {
			t.Errorf("subcommandTable[%q] declares subcommands %v but progkey does not treat it as subcommand-bearing: telemetry would record only %q and the meter could not tell its subcommands apart", program, subs, program)
		}
	}
}
