package hook

import (
	"strings"

	"github.com/synapctx/sctx/internal/application/run"
)

// Classify exposes the rewrite engine's decision for a command that has
// ALREADY run, so `sctx discover` can replay session history through the very
// rules the live hook applies. It adds no rule of its own: Rewrite decides
// "would be wrapped", gapSegment decides "uncovered". A wrapped command is keyed
// by run.CommandKey, the very key the run pipeline will have stored in the stats
// `command` column, so discover can match history against it; an uncovered one
// uses the coverage meter's deriveProgram. Either way a path or host can never
// reach a report through here.

// Verdict is what the hook would have done with a command.
type Verdict int

const (
	// VerdictSkip: not rewritten and not a coverage gap (already wrapped,
	// builtin, unsafe shape, deliberately unformatted, ...).
	VerdictSkip Verdict = iota
	// VerdictWrap: a rule applies, so the hook would have prefixed `sctx`.
	VerdictWrap
	// VerdictGap: a wrappable program sctx has no formatter for.
	VerdictGap
)

// Classify returns the verdict for cmd and the program key (program or
// "program subcommand", per progkey) of the segment concerned; the key is empty
// for VerdictSkip. The SCT__REWRITE_DISABLED kill switch is deliberately not
// honoured: discover asks what the rules would do, not whether they are on.
func Classify(cmd string) (Verdict, string) {
	if rewritten, ok := Rewrite(cmd); ok {
		// The rewrite only INSERTS "sctx " before each wrapped program. The
		// shared prefix can run past the insertion point (`sed` vs `sctx sed`
		// share "s"), so take the LAST word boundary inside it at which the
		// rewrite shows the inserted text.
		l := 0
		for l < len(cmd) && l < len(rewritten) && cmd[l] == rewritten[l] {
			l++
		}
		for p := l; p >= 0; p-- {
			boundary := p == 0 || strings.ContainsRune(" \t\n;&|", rune(cmd[p-1]))
			if boundary && strings.HasPrefix(rewritten[p:], "sctx ") {
				return VerdictWrap, wrapKey(cmd[p:])
			}
		}
		return VerdictWrap, wrapKey(cmd)
	}
	if seg, ok := gapSegment(cmd); ok {
		return VerdictGap, deriveProgram(seg)
	}
	return VerdictSkip, ""
}

// wrapKey is the stats key of the command that begins at the start of text:
// the first segment's argv, assignments dropped, run through run.CommandKey.
func wrapKey(text string) string {
	if segs, ok := splitSegments(text); ok && len(segs) > 0 {
		text = segs[0].text
	}
	tokens := tokenize(text)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	argv := make([]string, 0, len(tokens)-idx)
	for _, t := range tokens[idx:] {
		argv = append(argv, t.text)
	}
	return run.CommandKey(argv)
}
