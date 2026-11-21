package pytest

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Unittest renders `python -m unittest` output (the launcher keys such a run
// on the inner tool "unittest"). It lives beside pytest because both feed the
// same agent question — which tests failed and why — but the report is
// unrelated: dots, "=====" separated FAIL/ERROR blocks with a full traceback,
// then "Ran N tests" and an OK/FAILED verdict.
type Unittest struct{}

// NewUnittest constructs the unittest formatter.
func NewUnittest() *Unittest { return &Unittest{} }

// Descriptor claims the inner tool name "unittest".
func (u *Unittest) Descriptor() format.Match { return format.Match{Command: "unittest"} }

// Aggressive renders each failure as header, the innermost frame and the
// exception message; the passing rows and traceback frames above the
// innermost one drop with explicit counts.
func (u *Unittest) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return renderUnittest(in)
}

// Relaxed shares the aggressive rendering: the report grammar is fixed.
func (u *Unittest) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return renderUnittest(in)
}

var (
	utSepRe   = regexp.MustCompile(`^={70}$`)
	utRuleRe  = regexp.MustCompile(`^-{70}$`)
	utHeadRe  = regexp.MustCompile(`^(FAIL|ERROR): (.+)$`)
	utFrameRe = regexp.MustCompile(`^  File "(.+)", line (\d+), in (.+)$`)
	utCaretRe = regexp.MustCompile(`^\s*[~^]+\s*$`)
	utRanRe   = regexp.MustCompile(`^Ran \d+ tests? in [\d.]+s$`)
	utDotsRe  = regexp.MustCompile(`^[.EFsxu]+$`)
	utRowOKRe = regexp.MustCompile(` \.\.\. (ok|OK)$`)
)

// getwd is replaceable in tests.
var getwd = os.Getwd

func utRel(p string) string {
	wd, err := getwd()
	if err != nil || wd == "" {
		return p
	}
	return strings.TrimPrefix(p, strings.TrimRight(wd, "/")+"/")
}

func renderUnittest(in format.Input) (format.Rendered, error) {
	stderr, _ := readAll(in.Stderr)
	stdout, _ := readAll(in.Stdout)
	// unittest writes its whole report to stderr.
	text := strings.TrimRight(string(stderr)+string(stdout), "\r\n")
	if text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := strings.Split(text, "\n")
	var out []string
	ranIdx := -1
	okRows := 0
	elided := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		switch {
		case strings.TrimSpace(l) == "":
			continue
		case utSepRe.MatchString(l):
			continue
		case utRuleRe.MatchString(l):
			continue
		case utDotsRe.MatchString(l):
			continue // progress row: the verdict line carries the counts
		case utRowOKRe.MatchString(l):
			okRows++
			continue
		case utHeadRe.MatchString(l):
			out = append(out, l)
			// Body runs to the next separator, rule or end.
			j := i + 1
			if j < len(lines) && utRuleRe.MatchString(lines[j]) {
				j++
			}
			start := j
			for j < len(lines) && !utSepRe.MatchString(lines[j]) && !utRuleRe.MatchString(lines[j]) {
				j++
			}
			body, dropped := summariseTraceback(lines[start:j])
			out = append(out, body...)
			if dropped > 0 {
				elided = true
			}
			i = j - 1
		case utRanRe.MatchString(l):
			ranIdx = len(out)
			out = append(out, l)
		default:
			out = append(out, l)
		}
	}
	if ranIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if okRows > 0 {
		out = append(out, fmt.Sprintf("[%d passing rows omitted]", okRows))
		elided = true
	}
	return format.Rendered{Body: []byte(strings.Join(out, "\n")), FoldStderr: len(stderr) > 0, Elided: elided}, nil
}

// summariseTraceback keeps the innermost frame (location plus its source
// line) and the exception message, dropping caret rows and outer frames.
// A traceback it cannot model (chained exceptions) is kept whole, minus carets.
func summariseTraceback(body []string) (out []string, dropped int) {
	tb := 0
	for _, l := range body {
		if strings.HasPrefix(l, "Traceback (most recent call last):") {
			tb++
		}
	}
	var frames [][]string // each: frame line + source lines
	var tail []string
	inFrames := false
	for _, l := range body {
		switch {
		case strings.HasPrefix(l, "Traceback (most recent call last):"):
			inFrames = true
			if tb > 1 {
				out = append(out, l)
			}
		case utCaretRe.MatchString(l):
			dropped++
		case inFrames && utFrameRe.MatchString(l):
			frames = append(frames, []string{l})
		case inFrames && strings.HasPrefix(l, "    ") && len(frames) > 0:
			frames[len(frames)-1] = append(frames[len(frames)-1], l)
		default:
			inFrames = false
			if strings.TrimSpace(l) != "" {
				tail = append(tail, l)
			}
		}
	}
	if tb > 1 {
		for _, f := range frames {
			out = append(out, f...)
		}
		return append(out, tail...), dropped
	}
	if len(frames) > 0 {
		last := frames[len(frames)-1]
		m := utFrameRe.FindStringSubmatch(last[0])
		loc := fmt.Sprintf("  %s:%s in %s", utRel(m[1]), m[2], m[3])
		if len(last) > 1 {
			loc += ": " + strings.TrimSpace(last[1])
		}
		out = append(out, loc)
		if len(frames) > 1 {
			out = append(out, fmt.Sprintf("  …+%d outer frames", len(frames)-1))
			dropped += len(frames) - 1
		}
	}
	return append(out, tail...), dropped
}
