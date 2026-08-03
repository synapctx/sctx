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
func aggressiveStatus(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "On branch") &&
		!strings.HasPrefix(strings.TrimSpace(lines[0]), "HEAD detached") &&
		isPorcelainStatusLine(lines[0]) {
		return aggressiveStatusShort(raw, lines)
	}

	branch := "HEAD"
	ahead, behind := 0, 0

	var staged, modified, untracked []string
	section := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "On branch "):
			branch = strings.TrimPrefix(trimmed, "On branch ")
			continue
		case strings.HasPrefix(trimmed, "HEAD detached"):
			branch = trimmed
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
			switch section {
			case "staged":
				staged = append(staged, file)
			case "modified":
				modified = append(modified, file)
			case "untracked":
				untracked = append(untracked, file)
			}
		}
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

	if len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 {
		return format.Rendered{Body: []byte(head + ": clean")}, nil
	}

	var b strings.Builder
	b.WriteString(head)
	if len(staged) > 0 {
		fmt.Fprintf(&b, "\nstaged (%d): %s", len(staged), strings.Join(staged, ", "))
	}
	if len(modified) > 0 {
		fmt.Fprintf(&b, "\nmodified (%d): %s", len(modified), strings.Join(modified, ", "))
	}
	if len(untracked) > 0 {
		fmt.Fprintf(&b, "\nuntracked (%d): %s", len(untracked), strings.Join(untracked, ", "))
	}

	return format.Rendered{Body: []byte(b.String())}, nil
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
	var staged, modified, untracked []string

	for _, line := range lines {
		if !isPorcelainStatusLine(line) {
			continue
		}
		x, y := line[0], line[1]
		path := strings.TrimSpace(line[3:])
		switch {
		case x == '?' && y == '?':
			untracked = append(untracked, path)
		default:
			if x != ' ' {
				staged = append(staged, path)
			}
			if y != ' ' && y != '?' {
				modified = append(modified, path)
			}
		}
	}

	if len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	group := func(name string, files []string) string {
		if len(files) == 0 {
			return ""
		}
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

	var parts []string
	for _, g := range []string{
		group("staged", staged),
		group("modified", modified),
		group("untracked", untracked),
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
