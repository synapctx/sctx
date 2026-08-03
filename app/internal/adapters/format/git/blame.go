package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// blameLine is one parsed `git blame` output line:
// "<hash> (<author> <date> <time> <tz> <lineno>) <content>".
type blameLine struct {
	hash    string
	author  string
	lineno  int
	content string
}

// parseBlameLine parses a single default-format `git blame` line. It
// returns ok=false for anything that doesn't match the expected shape
// (e.g. --porcelain output), so the caller can bail out rather than guess.
func parseBlameLine(line string) (blameLine, bool) {
	idx := strings.Index(line, ")")
	if idx < 0 {
		return blameLine{}, false
	}
	prefix := line[:idx+1]
	content := strings.TrimPrefix(line[idx+1:], " ")

	open := strings.Index(prefix, "(")
	if open < 0 {
		return blameLine{}, false
	}
	hashField := strings.Fields(strings.TrimSpace(prefix[:open]))
	if len(hashField) == 0 {
		return blameLine{}, false
	}
	hash := hashField[0]

	parenContent := prefix[open+1 : len(prefix)-1]
	fields := strings.Fields(parenContent)
	if len(fields) < 4 {
		return blameLine{}, false
	}
	lineno, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return blameLine{}, false
	}
	author := strings.Join(fields[:len(fields)-4], " ")
	if author == "" {
		return blameLine{}, false
	}

	return blameLine{hash: hash, author: author, lineno: lineno, content: content}, true
}

// aggressiveBlame collapses consecutive `git blame` lines that share the
// same commit hash into a single "<hash> (<author>) L<start>-<end> ×N
// lines" entry, so a caller sees who/which-commit owns each region of the
// file instead of one line per source line.
func aggressiveBlame(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	parsed := make([]blameLine, 0, len(lines))
	for _, l := range lines {
		bl, ok := parseBlameLine(l)
		if !ok {
			// Unrecognized shape (e.g. --porcelain, --line-porcelain);
			// don't guess, let another tier handle it.
			return format.Rendered{}, format.ErrTierInapplicable
		}
		parsed = append(parsed, bl)
	}

	var out []string
	for i := 0; i < len(parsed); {
		j := i
		for j+1 < len(parsed) && parsed[j+1].hash == parsed[i].hash {
			j++
		}
		short := parsed[i].hash
		if len(short) > 7 {
			short = short[:7]
		}
		count := j - i + 1
		if count == 1 {
			out = append(out, fmt.Sprintf("%s (%s) L%d: %s", short, parsed[i].author, parsed[i].lineno, parsed[i].content))
		} else {
			out = append(out, fmt.Sprintf("%s (%s) L%d-%d ×%d lines", short, parsed[i].author, parsed[i].lineno, parsed[j].lineno, count))
		}
		i = j + 1
	}

	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d lines (%d regions)", len(parsed), len(out)),
	}, nil
}
