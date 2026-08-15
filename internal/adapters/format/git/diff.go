package git

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// diffStatSummaryRe matches the trailing summary line of `git diff --stat`
// output, e.g. "3 files changed, 12 insertions(+), 4 deletions(-)".
var diffStatSummaryRe = regexp.MustCompile(`^\s*\d+ files? changed`)

// diffStatFileCap bounds how many per-file --stat lines are kept before
// eliding the rest; the trailing summary line is always kept.
const diffStatFileCap = 30

// aggressiveDiff parses `git diff`/`git show` output (including
// --cached/--staged, which use the same unified-diff format), keeping file
// headers, hunk headers, and changed (+/-) lines, and dropping unchanged
// context lines. A trailing "N files changed" note is appended. When the
// output is a `--stat` summary instead (no unified-diff headers at all),
// the per-file stat lines are capped and the summary line is always kept.
func aggressiveDiff(in format.Input, args []string) (format.Rendered, error) {
	if diffArgsUnsafeToParse(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var kept []string
	filesChanged := 0
	sawDiff := false

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			sawDiff = true
			filesChanged++
			kept = append(kept, line)
		case !sawDiff:
			// `git show` prints commit metadata (commit/Author/Date/message)
			// before the first diff --git header; keep it verbatim.
			kept = append(kept, line)
		case strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file mode"),
			strings.HasPrefix(line, "deleted file mode"),
			strings.HasPrefix(line, "old mode"),
			strings.HasPrefix(line, "new mode"),
			strings.HasPrefix(line, "similarity index"),
			strings.HasPrefix(line, "dissimilarity index"),
			strings.HasPrefix(line, "copy from"),
			strings.HasPrefix(line, "copy to"),
			strings.HasPrefix(line, "rename from"),
			strings.HasPrefix(line, "rename to"),
			strings.HasPrefix(line, "Binary files "),
			strings.HasPrefix(line, "Submodule "),
			strings.HasPrefix(line, "\\ No newline at end of file"),
			strings.HasPrefix(line, "+++"),
			strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "@@"):
			kept = append(kept, line)
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			kept = append(kept, line)
		default:
			// Unchanged context line (starts with a space) or a
			// "\ No newline at end of file" marker: drop it.
		}
	}

	if filesChanged == 0 && !sawDiff {
		// No recognizable diff --git header at all: either a `--stat`
		// summary (handled below) or `git show` on a commit that only
		// touches binary files with no textual diff / an unexpected format.
		if rendered, ok := aggressiveDiffStat(raw, lines); ok {
			return rendered, nil
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if len(kept) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	note := fmt.Sprintf("%d files changed", filesChanged)
	kept = append(kept, note)

	return format.Rendered{
		Body: []byte(strings.Join(kept, "\n")),
		Note: note,
	}, nil
}

func diffArgsUnsafeToParse(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--binary", a == "--raw", a == "--numstat", a == "--shortstat",
			a == "--name-only", a == "--name-status", a == "--check", a == "--cc",
			a == "-c", a == "--combined-all-paths", strings.HasPrefix(a, "--word-diff"):
			return true
		}
	}
	return false
}

// aggressiveDiffStat caps a `git diff --stat` summary (per-file " path |
// N +++---" lines followed by a trailing "N files changed, ..." line) to
// diffStatFileCap file entries, always keeping the trailing summary line.
// ok is false when raw/lines don't look like a --stat summary at all, so
// the caller can fall back to ErrTierInapplicable.
func aggressiveDiffStat(raw []byte, lines []string) (format.Rendered, bool) {
	if len(lines) == 0 {
		return format.Rendered{}, false
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !diffStatSummaryRe.MatchString(last) {
		return format.Rendered{}, false
	}

	fileLines := lines[:len(lines)-1]
	shown := fileLines
	extra := 0
	if len(fileLines) > diffStatFileCap {
		extra = len(fileLines) - diffStatFileCap
		shown = fileLines[:diffStatFileCap]
	}
	if extra == 0 {
		// Nothing to elide; let the caller fall through so this doesn't
		// produce a non-smaller render.
		return format.Rendered{}, false
	}

	var out []string
	out = append(out, shown...)
	out = append(out, fmt.Sprintf("…+%d more files", extra))
	out = append(out, last)

	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, false
	}
	return format.Rendered{Body: []byte(body), Note: last}, true
}
