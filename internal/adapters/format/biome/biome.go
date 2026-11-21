// Package biome implements format.Formatter for the Biome toolchain
// (`biome check|lint|format|ci`) and for oxlint. Both print one multi-line
// diagnostic per finding — a message, a code frame, an explanation and often a
// fix diff — so the render keeps what a reader acts on (file, line:col, rule,
// severity, message) and counts the rest behind an explicit marker.
//
// Machine reporters (`--reporter=json|github|junit|...`, `--format=json|...`)
// are the user's own choice and decline.
package biome

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one linter's output.
type Formatter struct{ kind string }

// NewBiome constructs the biome formatter.
func NewBiome() *Formatter { return &Formatter{kind: "biome"} }

// NewOxlint constructs the oxlint formatter.
func NewOxlint() *Formatter { return &Formatter{kind: "oxlint"} }

// All returns the biome and oxlint formatters.
func All() []format.Formatter { return []format.Formatter{NewBiome(), NewOxlint()} }

// Descriptor claims every invocation of the tool's binary.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.kind} }

// Aggressive renders the grouped-diagnostics view.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineReporter(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	lines := cleanLines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))
	var (
		r  report
		ok bool
	)
	if f.kind == "oxlint" {
		r, ok = parseOxlint(lines)
	} else {
		r, ok = parseBiome(lines)
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return r.render(f.kind, len(stderr) > 0), nil
}

// Relaxed collapses provably redundant runs only.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineReporter(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	outLines, c1 := collapse.Runs(collapse.SplitLines(stdout))
	errLines, c2 := collapse.Runs(collapse.SplitLines(stderr))
	if !c1 && !c2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append(append([]string(nil), outLines...), errLines...)
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0}, nil
}

// machineReporter reports a user-selected non-default reporter.
func machineReporter(argv []string) bool {
	for i, a := range argv {
		var v string
		switch {
		case (a == "--reporter" || a == "--format" || a == "-f") && i+1 < len(argv):
			v = argv[i+1]
		case strings.HasPrefix(a, "--reporter="):
			v = strings.TrimPrefix(a, "--reporter=")
		case strings.HasPrefix(a, "--format="):
			v = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "--reporter-"), a == "--json":
			return true
		default:
			continue
		}
		if v != "default" && v != "pretty" && v != "unix" && v != "" {
			return true
		}
	}
	return false
}

var oscRE = regexp.MustCompile(`\x1b\]8;[^\x07\x1b]*(?:\x07|\x1b\\)`)

// cleanLines strips colour and OSC-8 hyperlinks, which biome emits around rule
// names when it believes it has a terminal.
func cleanLines(raw []byte) []string {
	return jsutil.Lines(oscRE.ReplaceAll(raw, nil))
}

// diag is one finding.
type diag struct {
	file, pos, rule, msg, sev string
	fixable                   bool
	n                         int // identical repeats folded in
}

type report struct {
	diags   []diag
	summary []string // tool summary lines kept verbatim
	hidden  int      // detail lines dropped (frames, hints, diffs)
	errs    int
	warns   int
	files   int
}

func (r *report) render(kind string, fold bool) format.Rendered {
	// Fold identical repeats, keep first-seen order.
	var uniq []diag
	idx := map[string]int{}
	for _, d := range r.diags {
		k := d.file + "|" + d.pos + "|" + d.rule + "|" + d.msg
		if i, ok := idx[k]; ok {
			uniq[i].n++
			continue
		}
		d.n = 1
		idx[k] = len(uniq)
		uniq = append(uniq, d)
	}
	uniq = groupByFile(uniq)
	var b strings.Builder
	var parts []string
	if r.errs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", r.errs, jsutil.Plural(r.errs, "error", "errors")))
	}
	if r.warns > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", r.warns, jsutil.Plural(r.warns, "warning", "warnings")))
	}
	if len(parts) == 0 {
		parts = append(parts, "no diagnostics")
	}
	b.WriteString(kind + ": " + strings.Join(parts, ", "))
	if r.files > 0 {
		fmt.Fprintf(&b, " in %d %s", r.files, jsutil.Plural(r.files, "file", "files"))
	}
	rules := map[string]int{}
	last := "\x00"
	for _, d := range uniq {
		if d.file != last {
			b.WriteString("\n" + d.file)
			last = d.file
		}
		b.WriteString("\n  ")
		if d.pos != "" {
			b.WriteString(d.pos + " ")
		}
		if d.sev != "error" {
			b.WriteString(d.sev + " ")
		}
		if d.rule != "" {
			b.WriteString(d.rule)
			rules[d.rule] += d.n
		}
		if d.msg != "" {
			b.WriteString(": " + d.msg)
		}
		if d.fixable {
			b.WriteString(" [fixable]")
		}
		if d.n > 1 {
			fmt.Fprintf(&b, " ×%d", d.n)
		}
	}
	if len(rules) > 1 && len(uniq) >= 6 {
		b.WriteString("\n" + jsutil.Histogram("rules", rules))
	}
	for _, s := range r.summary {
		b.WriteString("\n" + s)
	}
	elided := r.hidden > 0
	if r.hidden > 0 {
		fmt.Fprintf(&b, "\n…+%d detail lines (code frames, hints, fix diffs)", r.hidden)
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: elided}
}

// ---- biome ----

var (
	biomeHeadRE  = regexp.MustCompile(`^(\S+?)(?::(\d+):(\d+))?\s+(\S+)((?:\s+(?:FIXABLE|DEPRECATED|INTERNAL|UNSAFE))*)\s+━+\s*$`)
	biomeBareRE  = regexp.MustCompile(`^(\S+)\s+━+\s*$`)
	biomeSumRE   = regexp.MustCompile(`^(Checked|Formatted|Fixed|Linted) \d+ files? in `)
	biomeFoundRE = regexp.MustCompile(`^Found (\d+) (errors?|warnings?|infos?)\.$`)
	biomeLimitRE = regexp.MustCompile(`diagnostics (?:exceeds|not shown)`)
)

func severityOf(l string) (sev, msg string, ok bool) {
	t := strings.TrimSpace(l)
	for _, p := range []struct{ glyph, sev string }{
		{"×", "error"}, {"✖", "error"}, {"!", "warn"}, {"⚠", "warn"}, {"i", "info"}, {"ℹ", "info"},
	} {
		if strings.HasPrefix(t, p.glyph+" ") {
			return p.sev, strings.TrimSpace(t[len(p.glyph):]), true
		}
	}
	return "", "", false
}

func parseBiome(lines []string) (report, bool) {
	var r report
	files := map[string]bool{}
	var cur *diag
	var haveMsg, skipping bool
	var summaryErrs, summaryWarns = -1, -1
	finish := func() {
		if cur != nil {
			r.diags = append(r.diags, *cur)
			files[cur.file] = true
			switch cur.sev {
			case "error":
				r.errs++
			case "warn":
				r.warns++
			}
		}
		cur, haveMsg, skipping = nil, false, false
	}
	for _, l := range lines {
		if m := biomeHeadRE.FindStringSubmatch(l); m != nil {
			finish()
			d := diag{file: m[1], rule: m[4], fixable: strings.Contains(m[5], "FIXABLE")}
			if m[2] != "" {
				d.pos = m[2] + ":" + m[3]
			}
			cur = &d
			continue
		}
		if biomeBareRE.MatchString(l) {
			// Trailer category block ("check ━━━ × Some errors were emitted").
			finish()
			skipping = true
			continue
		}
		t := strings.TrimSpace(l)
		if skipping {
			continue
		}
		if m := biomeFoundRE.FindStringSubmatch(t); m != nil {
			finish()
			n, _ := strconv.Atoi(m[1])
			switch {
			case strings.HasPrefix(m[2], "error"):
				summaryErrs = n
			case strings.HasPrefix(m[2], "warning"):
				summaryWarns = n
			}
			continue
		}
		if biomeSumRE.MatchString(t) || biomeLimitRE.MatchString(t) || strings.HasPrefix(t, "Diagnostics not shown") {
			finish()
			r.summary = append(r.summary, t)
			continue
		}
		if cur != nil {
			if !haveMsg {
				if t == "" {
					continue
				}
				sev, msg, ok := severityOf(l)
				if !ok {
					return report{}, false
				}
				if strings.HasPrefix(msg, "Formatter would have printed") {
					msg = "needs formatting"
				}
				cur.sev, cur.msg, haveMsg = sev, msg, true
				continue
			}
			if t != "" {
				r.hidden++
			}
			continue
		}
		if t == "" {
			continue
		}
		// Unrecognised text outside any diagnostic: keep it rather than lose it.
		r.summary = append(r.summary, t)
	}
	finish()
	if len(r.diags) == 0 {
		return report{}, false
	}
	// The tool's own totals win when present (it may cap shown diagnostics).
	if summaryErrs >= 0 {
		r.errs = summaryErrs
	}
	if summaryWarns >= 0 {
		r.warns = summaryWarns
	}
	r.files = len(files)
	return r, true
}

// groupByFile keeps first-seen file order but makes each file's findings
// contiguous (biome reports per rule pass, so a file recurs).
func groupByFile(ds []diag) []diag {
	var order []string
	by := map[string][]diag{}
	for _, d := range ds {
		if _, ok := by[d.file]; !ok {
			order = append(order, d.file)
		}
		by[d.file] = append(by[d.file], d)
	}
	out := make([]diag, 0, len(ds))
	for _, f := range order {
		out = append(out, by[f]...)
	}
	return out
}
