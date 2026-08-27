package git

import (
	"fmt"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
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

	if logArgsAlreadyCompact(args) {
		return aggressiveLogCompact(raw, lines)
	}

	type commit struct {
		hash    string
		author  string
		date    string
		subject string
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
		case a == "-p", a == "-u", a == "--patch", a == "--stat", a == "--shortstat", a == "--numstat",
			a == "--name-only", a == "--name-status", a == "--raw", a == "--summary", a == "--graph":
			return true
		}
	}
	return false
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
