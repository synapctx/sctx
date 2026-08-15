package git

import (
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// progressRe matches git's transfer/pack progress lines, e.g.
// "Counting objects: 100% (5/5), done." or "Compressing objects:  50% (1/2)".
var progressRe = regexp.MustCompile(`(?i)\b\d{1,3}%\b`)

var progressPrefixes = []string{
	"Counting objects",
	"Compressing objects",
	"Writing objects",
	"Resolving deltas",
	"Enumerating objects",
	"remote: Counting objects",
	"remote: Compressing objects",
	"remote: Enumerating objects",
	"remote: Total",
	"Total ",
	"Receiving objects",
	"Unpacking objects",
	"Delta compression",
}

// isErrorLine reports whether a line carries substantive error/conflict
// content that must never be dropped, regardless of tier or exit code.
func isErrorLine(line string) bool {
	lower := strings.ToLower(line)
	for _, kw := range []string{"fatal:", "error:", "rejected", "conflict"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isHintOrAdviceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "(use ") {
		return true
	}
	if strings.HasPrefix(trimmed, "hint:") {
		return true
	}
	return false
}

func isProgressLine(line string) bool {
	if strings.ContainsRune(line, '\r') {
		return true
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "remote:"))
	trimmed = strings.TrimSpace(trimmed)
	for _, p := range progressPrefixes {
		if strings.HasPrefix(line, p) || strings.HasPrefix(strings.TrimPrefix(line, "remote: "), p) {
			return true
		}
	}
	if progressRe.MatchString(line) && (strings.Contains(line, "(") || strings.HasPrefix(line, "remote:") || trimmed == "") {
		return true
	}
	return false
}

// filterWriteNoise drops progress and hint/advice lines from add/commit/
// push/pull/fetch output while always keeping error/conflict content.
func filterWriteNoise(lines []string) []string {
	var out []string
	for _, line := range lines {
		if isHintOrAdviceLine(line) {
			continue
		}
		if isErrorLine(line) {
			out = append(out, line)
			continue
		}
		if isProgressLine(line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// aggressiveWrite handles add/commit/push/pull/fetch: it keeps the
// substantive result (commit summary, ref updates, fast-forward summary,
// rejected/error content) and drops transfer progress and advice/hints.
// Stderr is filtered the same way and folded into Body.
func aggressiveWrite(sub string, in format.Input) (format.Rendered, error) {
	// On failure, authentication messages, hooks, remote diagnostics and Git's
	// own hints are all actionable. Preserve the native streams verbatim.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)

	stdout := filterWriteNoise(splitLines(rawStdout))
	stderr := filterWriteNoise(splitLines(rawStderr))

	if len(stdout) == 0 && len(stderr) == 0 {
		if len(rawStdout) == 0 && len(rawStderr) == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		// A non-zero exit with nothing left after noise-filtering means the
		// error content didn't match our known error/conflict keywords;
		// never guess a "done" summary on failure. Degrade to relaxed,
		// which keeps the raw lines instead of filtering them away.
		if in.ExitCode != 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		// Everything was noise; still must not return an empty body for
		// non-empty raw input, so fall back to a minimal summary line.
		return format.Rendered{
			Body:       []byte(sub + ": done"),
			FoldStderr: true,
		}, nil
	}

	var body []string
	body = append(body, stdout...)
	body = append(body, stderr...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: true,
	}, nil
}
