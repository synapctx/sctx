package git

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/difffloor"
)

// gitDateLayout matches the default `git log` author date format, e.g.
// "Mon Jan 2 15:04:05 2006 -0700".
const gitDateLayout = "Mon Jan 2 15:04:05 2006 -0700"

// logArgsAlreadyCompact reports whether the user already asked git for a
// compact/custom log format, in which case reformatting adds nothing.
func logArgsAlreadyCompact(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--oneline":
			return true
		case a == "--pretty=oneline":
			return true
		}
	}
	return false
}

// logCompactCap is the number of commits kept when `git log` was already
// asked for a compact form (--oneline/--format/--pretty); this is the most
// common log invocation and must still be capped for long histories.
const logCompactCap = 30

// aggressiveLogCompact caps an already-compact log (one line per commit,
// e.g. --oneline or a custom --format/--pretty) to the most recent
// logCompactCap commits, replacing the rest with an explicit elision
// marker. If the output is already at or under the cap there is nothing to
// gain, so the tier is inapplicable.
func aggressiveLogCompact(raw []byte, lines []string) (format.Rendered, error) {
	if len(lines) <= logCompactCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept := lines[:logCompactCap]
	remaining := len(lines) - logCompactCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more commits", remaining)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d commits (%d shown)", len(lines), logCompactCap),
	}, nil
}

// aggressiveLog parses the default `git log` output into one compact line
// per commit: "<short-hash> <subject> (<author>, <relative-date>)". When the
// caller already requested a compact/custom format (one line per commit),
// the content is left as-is and only capped to the most recent commits.
func aggressiveLog(in format.Input, args []string) (format.Rendered, error) {
	if logArgsUnsafeToParse(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if hasAnyArg(args, "-p", "-u", "--patch") {
		return aggressiveLogPatch(raw, lines, args)
	}

	if logArgsAlreadyCompact(args) {
		if hasAnyArg(args, "--stat", "--shortstat", "--numstat", "--name-only", "--name-status") {
			// Per-file lines interleave with the one-line records; counting
			// lines as commits would be wrong.
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveLogCompact(raw, lines)
	}

	type commit struct {
		hash    string
		author  string
		date    string
		subject string
		files   []string // --stat / --name-only / --numstat lines, in order
	}

	var commits []commit
	var cur *commit

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "commit "):
			if cur != nil {
				commits = append(commits, *cur)
			}
			hash := strings.TrimSpace(strings.TrimPrefix(line, "commit "))
			hash = strings.Fields(hash)[0] // drop "(HEAD -> main, ...)" decorations
			cur = &commit{hash: hash}
		case strings.HasPrefix(line, "Author:"):
			if cur == nil {
				continue
			}
			author := strings.TrimSpace(strings.TrimPrefix(line, "Author:"))
			if i := strings.Index(author, "<"); i >= 0 {
				author = strings.TrimSpace(author[:i])
			}
			cur.author = author
		case strings.HasPrefix(line, "Date:"):
			if cur == nil {
				continue
			}
			cur.date = strings.TrimSpace(strings.TrimPrefix(line, "Date:"))
		case strings.HasPrefix(line, "    "):
			if cur == nil || cur.subject != "" {
				continue // only take the first non-blank body line as subject
			}
			subject := strings.TrimSpace(line)
			if subject != "" {
				cur.subject = subject
			}
		case strings.HasPrefix(line, "Merge:"), strings.TrimSpace(line) == "":
		default:
			// A per-file line requested by --stat/--numstat/--name-only/
			// --name-status: keep it under its commit.
			if cur != nil {
				cur.files = append(cur.files, strings.TrimSpace(line))
			}
		}
	}
	if cur != nil {
		commits = append(commits, *cur)
	}
	if len(commits) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	shown := commits
	extra := 0
	if len(commits) > logCompactCap {
		shown = commits[:logCompactCap]
		extra = len(commits) - logCompactCap
	}

	var b strings.Builder
	for i, c := range shown {
		if i > 0 {
			b.WriteByte('\n')
		}
		short := c.hash
		if len(short) > 7 {
			short = short[:7]
		}
		when := c.date
		if t, err := time.Parse(gitDateLayout, c.date); err == nil {
			when = relativeTime(t)
		}
		fmt.Fprintf(&b, "%s %s (%s, %s)", short, c.subject, c.author, when)
		writeLogFiles(&b, c.files)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "\n…+%d more commits", extra)
	}

	return format.Rendered{Body: []byte(b.String()), Note: fmt.Sprintf("%d commits (%d shown)", len(commits), len(shown))}, nil
}

func logArgsUnsafeToParse(args []string) bool {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--format"), strings.HasPrefix(a, "--pretty") && a != "--pretty=oneline":
			return true
		case a == "--raw", a == "--summary", a == "--graph",
			strings.HasPrefix(a, "--stat="), strings.HasPrefix(a, "--dirstat"):
			return true
		}
	}
	return false
}

// logFilesPerCommitCap bounds the per-file lines kept under each commit when
// the log was asked for --stat/--numstat/--name-only; the trailing "N files
// changed" summary is always kept.
const logFilesPerCommitCap = 12

var logStatSummaryRe = regexp.MustCompile(`^\d+ files? changed`)

// squeezeStatPadding removes the column alignment git pads a --stat line
// with ("name      |  16 ++--" becomes "name | 16 ++--"); the name is untouched.
func squeezeStatPadding(l string) string {
	i := strings.Index(l, " | ")
	if i < 0 {
		return l
	}
	return strings.TrimRight(l[:i], " ") + " | " + strings.TrimLeft(l[i+3:], " ")
}

// writeLogFiles writes a commit's per-file lines, indented under its header
// line, eliding the excess with an explicit marker.
func writeLogFiles(b *strings.Builder, files []string) {
	shown := 0
	extra := 0
	for _, f := range files {
		if logStatSummaryRe.MatchString(f) {
			continue
		}
		if shown >= logFilesPerCommitCap {
			extra++
			continue
		}
		b.WriteString("\n  ")
		b.WriteString(squeezeStatPadding(f))
		shown++
	}
	if extra > 0 {
		fmt.Fprintf(b, "\n  …+%d files", extra)
	}
	for _, f := range files {
		if logStatSummaryRe.MatchString(f) {
			b.WriteString("\n  ")
			b.WriteString(f)
		}
	}
}

// relativeTime renders a coarse human-relative duration from t to now,
// matching git's own "N units ago" style closely enough for a compact log.
func relativeTime(t time.Time) string {
	d := max(time.Since(t), 0)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		return plural(n, "minute") + " ago"
	case d < 24*time.Hour:
		n := int(d / time.Hour)
		return plural(n, "hour") + " ago"
	case d < 30*24*time.Hour:
		n := int(d / (24 * time.Hour))
		return plural(n, "day") + " ago"
	case d < 365*24*time.Hour:
		n := int(d / (30 * 24 * time.Hour))
		return plural(n, "month") + " ago"
	default:
		n := int(d / (365 * 24 * time.Hour))
		return plural(n, "year") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

var logCommitLineRe = regexp.MustCompile(`^commit [0-9a-f]{40,64}\b`)

// aggressiveLogPatch renders `git log -p`: each commit's metadata collapses to
// the one-line header, and its diff is compacted exactly like `git diff`
// (one line of context around each change). Below the diff fidelity floor, or
// for anything that does not parse, the output passes through verbatim.
func aggressiveLogPatch(raw []byte, lines []string, args []string) (format.Rendered, error) {
	if hasAnyArg(args, "--stat", "--shortstat", "--numstat", "--name-only", "--name-status", "--oneline") || logArgsUnsafeToParse(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if !difffloor.Text(raw) || difffloor.Below(len(lines)) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	type block struct {
		hash, author, date, subject string
		diff                        []string
	}
	var blocks []*block
	var cur *block
	inDiff := false
	for _, line := range lines {
		switch {
		case logCommitLineRe.MatchString(line):
			cur = &block{hash: strings.Fields(strings.TrimPrefix(line, "commit "))[0]}
			blocks = append(blocks, cur)
			inDiff = false
		case cur == nil:
			return format.Rendered{}, format.ErrTierInapplicable
		case strings.HasPrefix(line, "diff --git"):
			inDiff = true
			cur.diff = append(cur.diff, line)
		case inDiff:
			cur.diff = append(cur.diff, line)
		case strings.HasPrefix(line, "Author:"):
			a := strings.TrimSpace(strings.TrimPrefix(line, "Author:"))
			if i := strings.Index(a, "<"); i >= 0 {
				a = strings.TrimSpace(a[:i])
			}
			cur.author = a
		case strings.HasPrefix(line, "Date:"):
			cur.date = strings.TrimSpace(strings.TrimPrefix(line, "Date:"))
		case strings.HasPrefix(line, "    "):
			if cur.subject == "" {
				cur.subject = strings.TrimSpace(line)
			}
		}
	}
	if len(blocks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var out []string
	for _, b := range blocks {
		short := b.hash
		if len(short) > 7 {
			short = short[:7]
		}
		when := b.date
		if t, err := time.Parse(gitDateLayout, b.date); err == nil {
			when = relativeTime(t)
		}
		out = append(out, fmt.Sprintf("%s %s (%s, %s)", short, b.subject, b.author, when))
		for i, l := range b.diff {
			if keepDiffBodyLine(b.diff, i) {
				out = append(out, l)
			}
		}
	}
	out = append([]string{difffloor.Stamp(len(lines), len(out)+1)}, out...)
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d commits", len(blocks))}, nil
}
