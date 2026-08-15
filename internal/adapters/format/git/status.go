package git

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	aheadRe    = regexp.MustCompile(`ahead of .* by (\d+) commit`)
	behindRe   = regexp.MustCompile(`behind .* by (\d+) commit`)
	divergedRe = regexp.MustCompile(`have (\d+) and (\d+) different commits`)
)

// statusEntry status-word prefixes used inside "Changes to be committed" and
// "Changes not staged for commit" blocks.
var statusWordPrefixes = []string{
	"new file:", "modified:", "deleted:", "renamed:", "copied:",
	"both modified:", "both added:", "added by us:", "added by them:",
	"deleted by us:", "deleted by them:", "both deleted:",
}

// statusShortEntryCap bounds how many paths are listed per status group
// (staged/modified/untracked) for `git status --short`/`-s`/`--porcelain`
// output before eliding the rest.
const statusShortEntryCap = 30

// isPorcelainStatusLine reports whether line looks like one entry of
// `git status --short`/`--porcelain` output: a 2-character XY status code
// followed by a space and a path (e.g. "M  a.txt", "?? d.txt",
// " M e.txt", "R  old.txt -> new.txt").
func isPorcelainStatusLine(line string) bool {
	if len(line) < 4 || line[2] != ' ' {
		return false
	}
	const codes = " MADRCU?!"
	return strings.ContainsRune(codes, rune(line[0])) && strings.ContainsRune(codes, rune(line[1]))
}

// aggressiveStatus parses `git status` output into a compact branch/section
// summary. It handles both the default human-readable form and
// --short/-s/--porcelain output.
func aggressiveStatus(in format.Input, args []string) (format.Rendered, error) {
	if hasAnyArg(args, "-z", "--null") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if statusPorcelainV2(args) {
		return aggressiveStatusPorcelainV2(raw, lines)
	}
	if statusShort(args) || isPorcelainStatusLine(lines[0]) || strings.HasPrefix(lines[0], "## ") {
		return aggressiveStatusShort(raw, lines)
	}

	branch := "HEAD"
	ahead, behind := 0, 0

	var staged, modified, untracked, ignored, conflicted []string
	var operation string
	section := ""
	sawHeader := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "On branch "):
			branch = strings.TrimPrefix(trimmed, "On branch ")
			sawHeader = true
			continue
		case strings.HasPrefix(trimmed, "HEAD detached"):
			branch = trimmed
			sawHeader = true
			continue
		case trimmed == "Not currently on any branch.":
			branch = "HEAD detached"
			sawHeader = true
			continue
		case isOperationStatusLine(trimmed):
			if operation == "" {
				operation = trimmed
			}
			sawHeader = true
			continue
		case strings.HasPrefix(trimmed, "Changes to be committed"):
			section = "staged"
			continue
		case strings.HasPrefix(trimmed, "Changes not staged for commit"):
			section = "modified"
			continue
		case strings.HasPrefix(trimmed, "Untracked files"):
			section = "untracked"
			continue
		case strings.HasPrefix(trimmed, "Ignored files"):
			section = "ignored"
			continue
		case strings.HasPrefix(trimmed, "Unmerged paths"):
			section = "modified"
			continue
		}

		if m := divergedRe.FindStringSubmatch(trimmed); m != nil {
			fmt.Sscanf(m[1], "%d", &ahead)
			fmt.Sscanf(m[2], "%d", &behind)
			continue
		}
		if m := aheadRe.FindStringSubmatch(trimmed); m != nil {
			fmt.Sscanf(m[1], "%d", &ahead)
			continue
		}
		if m := behindRe.FindStringSubmatch(trimmed); m != nil {
			fmt.Sscanf(m[1], "%d", &behind)
			continue
		}

		if strings.HasPrefix(trimmed, "(use ") {
			continue
		}
		if trimmed == "" {
			continue
		}

		if section != "" && strings.HasPrefix(line, "\t") {
			file := statusFile(trimmed)
			if section == "modified" && isConflictStatusWord(trimmed) {
				conflicted = append(conflicted, file)
				continue
			}
			switch section {
			case "staged":
				staged = append(staged, file)
			case "modified":
				modified = append(modified, file)
			case "untracked":
				untracked = append(untracked, file)
			case "ignored":
				ignored = append(ignored, file)
			}
		}
	}
	if !sawHeader {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	head := branch
	switch {
	case ahead > 0 && behind > 0:
		head += fmt.Sprintf(" (ahead %d, behind %d)", ahead, behind)
	case ahead > 0:
		head += fmt.Sprintf(" (ahead %d)", ahead)
	case behind > 0:
		head += fmt.Sprintf(" (behind %d)", behind)
	}

	if operation == "" && len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 && len(ignored) == 0 && len(conflicted) == 0 {
		return format.Rendered{Body: []byte(head + ": clean")}, nil
	}

	var b strings.Builder
	b.WriteString(head)
	if operation != "" {
		b.WriteString("\noperation: ")
		b.WriteString(operation)
	}
	if len(staged) > 0 {
		b.WriteByte('\n')
		b.WriteString(statusGroup("staged", staged))
	}
	if len(modified) > 0 {
		b.WriteByte('\n')
		b.WriteString(statusGroup("modified", modified))
	}
	if len(conflicted) > 0 {
		b.WriteByte('\n')
		b.WriteString(statusGroup("conflicted", conflicted))
	}
	if len(untracked) > 0 {
		b.WriteByte('\n')
		b.WriteString(statusGroup("untracked", untracked))
	}
	if len(ignored) > 0 {
		b.WriteByte('\n')
		b.WriteString(statusGroup("ignored", ignored))
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}

func isOperationStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "rebase in progress") ||
		strings.HasPrefix(lower, "you are currently rebasing") ||
		strings.HasPrefix(lower, "you are currently cherry-picking") ||
		strings.HasPrefix(lower, "you are currently reverting") ||
		strings.HasPrefix(lower, "all conflicts fixed but you are still merging") ||
		strings.HasPrefix(lower, "you are in the middle of a bisect session")
}

func statusGroup(name string, files []string) string {
	shown := files
	extra := 0
	if len(files) > statusShortEntryCap {
		extra = len(files) - statusShortEntryCap
		shown = files[:statusShortEntryCap]
	}
	s := fmt.Sprintf("%s (%d): %s", name, len(files), strings.Join(shown, ", "))
	if extra > 0 {
		s += fmt.Sprintf(", …+%d", extra)
	}
	return s
}

func isConflictStatusWord(line string) bool {
	for _, prefix := range []string{"both modified:", "both added:", "added by us:", "added by them:", "deleted by us:", "deleted by them:", "both deleted:"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func statusShort(args []string) bool {
	return hasAnyArg(args, "-s", "--short", "--porcelain", "--porcelain=v1", "--porcelain=1")
}

func statusPorcelainV2(args []string) bool {
	return hasAnyArg(args, "--porcelain=v2", "--porcelain=2")
}

// statusFile extracts the file path from a status entry line such as
// "modified:   a.txt" or a bare untracked path like "d.txt".
func statusFile(trimmed string) string {
	for _, prefix := range statusWordPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return trimmed
}

// aggressiveStatusShort groups `git status --short`/`-s`/`--porcelain`
// entries into staged/modified/untracked buckets by their XY status code,
// capping each bucket at statusShortEntryCap paths.
func aggressiveStatusShort(raw []byte, lines []string) (format.Rendered, error) {
	var branch string
	var staged, modified, untracked, ignored, conflicted []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			branch = strings.TrimPrefix(line, "## ")
			continue
		}
		if !isPorcelainStatusLine(line) {
			continue
		}
		x, y := line[0], line[1]
		path := strings.TrimSpace(line[3:])
		switch {
		case x == '?' && y == '?':
			untracked = append(untracked, path)
		case x == '!' && y == '!':
			ignored = append(ignored, path)
		case isConflictCode(x, y):
			conflicted = append(conflicted, path)
		default:
			if x != ' ' {
				staged = append(staged, path)
			}
			if y != ' ' && y != '?' {
				modified = append(modified, path)
			}
		}
	}

	if branch == "" && len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 && len(ignored) == 0 && len(conflicted) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var parts []string
	if branch != "" {
		parts = append(parts, branch)
	}
	for _, g := range []string{
		statusGroupIfAny("staged", staged),
		statusGroupIfAny("modified", modified),
		statusGroupIfAny("conflicted", conflicted),
		statusGroupIfAny("untracked", untracked),
		statusGroupIfAny("ignored", ignored),
	} {
		if g != "" {
			parts = append(parts, g)
		}
	}

	body := strings.Join(parts, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

func statusGroupIfAny(name string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	return statusGroup(name, files)
}

func isConflictCode(x, y byte) bool {
	switch string([]byte{x, y}) {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	default:
		return false
	}
}

// Porcelain v2 is already compact and stable. For unusually large results,
// preserve its records verbatim and only cap the record count; headers remain.
func aggressiveStatusPorcelainV2(raw []byte, lines []string) (format.Rendered, error) {
	var headers, records []string
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			headers = append(headers, line)
			continue
		}
		if strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 ") || strings.HasPrefix(line, "u ") || strings.HasPrefix(line, "? ") || strings.HasPrefix(line, "! ") {
			records = append(records, line)
			continue
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(records) <= statusShortEntryCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := append(append([]string{}, headers...), records[:statusShortEntryCap]...)
	out = append(out, fmt.Sprintf("…+%d more status records", len(records)-statusShortEntryCap))
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d status records (%d shown)", len(records), statusShortEntryCap)}, nil
}
