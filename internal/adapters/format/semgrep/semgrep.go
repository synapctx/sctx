// Package semgrep implements a format.Formatter for semgrep's default text
// report (`semgrep scan`, `semgrep ci`). Findings are grouped per file with
// rule id, line and message; code excerpts, "Details:" links and autofix
// lines are counted and dropped, and the stderr scan-status banner collapses
// to the result lines.
package semgrep

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders semgrep output.
type Formatter struct{}

// New constructs a semgrep Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every semgrep invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "semgrep"} }

var machineFlags = []string{"--json", "--sarif", "--emacs", "--vim", "--junit-xml", "--gitlab-sast", "--gitlab-secrets", "--rdjson", "--sarif-output", "--json-output"}

func machineOutput(argv []string) bool {
	if lintkit.HasFlag(argv, machineFlags...) {
		return true
	}
	for _, a := range argv[1:] {
		for _, f := range machineFlags {
			if strings.HasPrefix(a, f+"=") && (f != "--sarif-output" && f != "--json-output") {
				return true
			}
		}
	}
	_, out := lintkit.OptionValue(argv, "-o", "--output")
	return out
}

var (
	findingRE = regexp.MustCompile(`^\s*(❯*)❱\s+(\S+)\s*$`)
	codeRE    = regexp.MustCompile(`^\s*(\d+)┆`)
	fileRE    = regexp.MustCompile(`^ {4}(\S.*?)\s*$`)
	headerRE  = regexp.MustCompile(`^│ .* │$`)
)

type finding struct {
	rule, sev, msg string
	line           int
}

// Aggressive parses the report. `semgrep scan` exits 0 on findings unless
// --error is given, so recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)

	var order []string
	by := map[string][]finding{}
	var curFile string
	var cur *finding
	var msg []string
	dropped := 0
	total := 0
	flush := func() {
		if cur != nil {
			cur.msg = lintkit.Clip(strings.Join(msg, " "))
			by[curFile] = append(by[curFile], *cur)
			total++
		}
		cur, msg = nil, nil
	}
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			continue
		case strings.HasPrefix(t, "┌") || strings.HasPrefix(t, "└") || (headerRE.MatchString(t) && strings.Contains(t, "Findings")):
			flush()
			continue
		}
		if m := findingRE.FindStringSubmatch(l); m != nil {
			flush()
			if curFile == "" {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			sev := "info"
			switch strings.Count(m[1], "❯") {
			case 1:
				sev = "warning"
			case 2, 3:
				sev = "error"
			}
			cur = &finding{rule: m[2], sev: sev}
			continue
		}
		if m := codeRE.FindStringSubmatch(l); m != nil {
			if cur == nil {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			if cur.line == 0 {
				cur.line, _ = strconv.Atoi(m[1])
			}
			dropped++
			continue
		}
		if strings.HasPrefix(t, "▶▶┆") {
			dropped++
			continue
		}
		if m := fileRE.FindStringSubmatch(l); m != nil {
			flush()
			curFile = m[1]
			if _, ok := by[curFile]; !ok {
				order = append(order, curFile)
			}
			continue
		}
		if cur == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(t, "Details: http") {
			dropped++
			continue
		}
		msg = append(msg, t)
	}
	flush()
	errBody, errDropped, summary := filterStderr(string(errRaw))
	if total == 0 && !summary {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	if total == 0 {
		b.WriteString("semgrep: 0 findings")
	} else {
		fmt.Fprintf(&b, "semgrep: %d findings in %d files", total, len(order))
	}
	for _, file := range order {
		fmt.Fprintf(&b, "\n%s (%d)", file, len(by[file]))
		for _, f := range by[file] {
			fmt.Fprintf(&b, "\n  %s %s %s %s", lintkit.Pos(f.line, 0), f.sev, f.rule, f.msg)
		}
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "\n…+%d code excerpt/details/autofix lines dropped", dropped)
	}
	if errBody != "" {
		b.WriteString("\n" + errBody)
	}
	if errDropped > 0 {
		fmt.Fprintf(&b, "\n…+%d stderr banner lines dropped", errDropped)
	}
	body := b.String()
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: dropped+errDropped > 0,
		Note: fmt.Sprintf("%d findings", total)}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

var noiseRE = regexp.MustCompile(`^(Scan Status|Scan Summary|✅ Scan completed successfully\.|•? ?(Rules run|Targets scanned|Parsed lines|No ignore information available)\b.*|Scanning \d+ files? .*|A new version of Semgrep is available.*|If Semgrep missed a finding.*|See https://semgrep\.dev/docs/reporting-false-negatives.*|For a detailed list of skipped files.*)$`)

// filterStderr keeps result lines and every line it does not recognise as
// banner noise. summary reports whether a scan-summary banner was seen.
func filterStderr(s string) (body string, dropped int, summary bool) {
	var keep []string
	for _, l := range lintkit.Lines([]byte(s)) {
		l = lintkit.StripANSI(l)
		t := strings.TrimSpace(l)
		t = strings.TrimSpace(strings.TrimLeft(t, "•"))
		if t == "" || strings.Trim(t, "┌┐└┘─ ") == "" || (strings.HasPrefix(t, "│") && strings.HasSuffix(t, "│")) {
			if t != "" {
				dropped++
			}
			if strings.Contains(t, "Scan Summary") {
				summary = true
			}
			continue
		}
		if strings.HasPrefix(t, "Ran ") {
			summary = true
		}
		if noiseRE.MatchString(t) {
			dropped++
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, "\n"), dropped, summary
}
