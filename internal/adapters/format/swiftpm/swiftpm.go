// Package swiftpm formats `swift build` and `swift test`. The build system's
// "[12 / 40] Demo" (thin-space separated) progress lines collapse into a counted marker; each
// compiler diagnostic keeps its "path:line:col: severity: message" line and
// loses the source excerpt, the restated caret message and the documentation
// link footer (all counted, via the cc package); the multi-kilobyte command
// lines in "error: SwiftCompile ... failed" and "Failed frontend command:" are
// truncated with an explicit character count. Output of the test run itself
// (XCTest / swift-testing result lines) is not recognised and passes through.
package swiftpm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/cc"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	progressRE = regexp.MustCompile(`^\[(?:Pre-planning |Planning )?\d+\p{Zs}*/\p{Zs}*\d+\](?: .*)?$|^\[Planning deferred tasks\]$`)
	docLinkRE  = regexp.MustCompile(`^\[#\w+\]: <https?://[^>]*>$`)
)

const maxLine = 160

// Formatter renders swift build / swift test output.
type Formatter struct{}

// New constructs a swift Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every swift invocation; only build and test render.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "swift"} }

// Aggressive collapses progress, filters diagnostics and truncates commands.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed collapses progress and truncates commands only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func handled(argv []string) bool {
	sub := ""
	for _, a := range argv[1:] {
		if a == "--version" || a == "-version" || a == "--help" || a == "-h" || a == "-v" || a == "--verbose" ||
			a == "-vv" || a == "--very-verbose" || a == "--show-bin-path" || a == "--list-tests" || a == "-l" ||
			a == "--show-codecov-path" || a == "--print-pif-manifest" || a == "--print-manifest-job-graph" {
			return false
		}
		if sub == "" && !strings.HasPrefix(a, "-") {
			sub = a
		}
	}
	return sub == "build" || sub == "test" || sub == "b" || sub == "t"
}

func render(in format.Input, diagnostics bool) (format.Rendered, error) {
	if !handled(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := cc.ReadAll(in.Stdout), cc.ReadAll(in.Stderr)
	lines := append(cc.SplitLines(rawOut), cc.SplitLines(rawErr)...)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	changed := false
	var st cc.Stats
	if diagnostics {
		lines, st = cc.Filter(lines, true)
		changed = st.Dropped > 0 || st.Deduped > 0
	} else {
		for i, l := range lines {
			lines[i] = cc.StripANSI(l)
		}
	}

	var out []string
	progress, links := 0, 0
	flush := func() {
		if progress > 0 {
			out = append(out, fmt.Sprintf("…+%d progress lines", progress))
			changed = true
		}
		progress = 0
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case progressRE.MatchString(l):
			progress++
			continue
		case diagnostics && docLinkRE.MatchString(l):
			links++
			changed = true
			continue
		case strings.TrimSpace(l) == "" && diagnostics && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == ""):
			continue
		}
		flush()
		switch {
		case l == "Failed frontend command:" && i+1 < len(lines):
			// The next line is the whole swift-frontend invocation.
			out = append(out, fmt.Sprintf("Failed frontend command: …+%d chars", len(lines[i+1])))
			i++
			changed = true
			continue
		case strings.HasPrefix(l, "error: ") && len(l) > maxLine:
			l = fmt.Sprintf("%s …+%d chars", l[:maxLine], len(l)-maxLine)
			changed = true
		}
		out = append(out, l)
	}
	flush()
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if diagnostics {
		out = append(out, cc.Footer(st, true)...)
		if links > 0 {
			out = append(out, fmt.Sprintf("…+%d documentation links", links))
		}
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}
