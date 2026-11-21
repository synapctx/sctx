// Package trivy implements a format.Formatter for trivy's default table
// report (image, fs, rootfs, repo, config, sbom, vm). Every CRITICAL and HIGH
// finding is kept with its id, package, installed and fixed versions;
// MEDIUM/LOW/UNKNOWN vulnerabilities are reduced to per-package counts, and
// misconfiguration/secret blocks keep id, severity, message and location while
// their long descriptions and code excerpts are counted and dropped. Progress
// bars, INFO log lines and the update notice on stderr are counted and
// dropped; WARN/ERROR/FATAL lines are kept.
package trivy

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders trivy output.
type Formatter struct{}

// New constructs a trivy Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every trivy invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "trivy"} }

// machineOutput reports flags that select a non-table report or divert it.
func machineOutput(argv []string) bool {
	if v, ok := lintkit.OptionValue(argv, "-f", "--format"); ok && v != "table" {
		return true
	}
	if _, ok := lintkit.OptionValue(argv, "-o", "--output"); ok {
		return true
	}
	if _, ok := lintkit.OptionValue(argv, "--template"); ok {
		return true
	}
	return false
}

// Aggressive parses the table report.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	rep, ok := parseReport(lintkit.Lines(raw))
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := rep.render()
	if logs, dropped := filterStderr(string(errRaw)); logs != "" || dropped > 0 {
		if logs != "" {
			body += "\n" + logs
		}
		if dropped > 0 {
			body += fmt.Sprintf("\n…+%d stderr lines (progress, INFO logs, update notice)", dropped)
		}
	}
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("%d targets", len(rep.targets))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

var (
	logLineRE   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[\d:.]+(?:Z|[+-]\d\d:\d\d)\t([A-Z]+)\t(.*)$`)
	progressRE  = regexp.MustCompile(`\d+(?:\.\d+)? [KMG]?i?B / \d+(?:\.\d+)? [KMG]?i?B`)
	noticeStart = "📣"
)

// filterStderr keeps WARN/ERROR/FATAL log lines (timestamp removed) and every
// line it does not recognise; INFO lines, download progress and the update
// notice are counted. It returns the kept text and the number dropped.
func filterStderr(s string) (string, int) {
	var keep []string
	dropped := 0
	inNotice := false
	for _, l := range lintkit.Lines([]byte(strings.ReplaceAll(s, "\r", "\n"))) {
		l = lintkit.StripANSI(l)
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			continue
		case progressRE.MatchString(t):
			dropped++
			continue
		case strings.HasPrefix(t, noticeStart):
			inNotice = true
			dropped++
			continue
		case inNotice && (strings.HasPrefix(t, "- Version") || strings.HasPrefix(t, "To suppress version checks")):
			dropped++
			continue
		}
		inNotice = false
		if m := logLineRE.FindStringSubmatch(l); m != nil {
			if m[1] == "INFO" || m[1] == "DEBUG" {
				dropped++
				continue
			}
			keep = append(keep, m[1]+" "+strings.ReplaceAll(m[2], "\t", " "))
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n"), dropped
}

// ---- stdout report model ----

type vuln struct {
	lib, id, sev, status, installed, fixed string
}

type misconf struct {
	id, sev, msg, loc string
}

type section struct {
	name    string // "alpine:3.10 (alpine 3.10.9)"
	totals  []string
	vulns   []vuln
	issues  []misconf // misconfigurations and secrets
	other   []string  // unrecognised lines kept verbatim
	dropped int       // description/code lines
}

type report struct {
	summary []string
	targets []*section
	legend  int
}

var (
	equalsRE   = regexp.MustCompile(`^={3,}$`)
	dblRE      = regexp.MustCompile(`^═{5,}$`)
	sepRE      = regexp.MustCompile(`^─{5,}$`)
	misBlockRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+) \((CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN)\): (.*)$`)
	secBlockRE = regexp.MustCompile(`^(CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN): (.*)$`)
	totalsRE   = regexp.MustCompile(`^(Total|Tests|Failures): `)
)

func isBox(l string) bool {
	return l != "" && strings.ContainsRune("┌├└│", []rune(l)[0])
}

func parseReport(lines []string) (*report, bool) {
	rep := &report{}
	var cur *section
	saw := false
	for i := 0; i < len(lines); i++ {
		l := lintkit.StripANSI(lines[i])
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			continue
		case t == "Report Summary":
			saw = true
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			k := j
			for k < len(lines) && isBox(lines[k]) {
				k++
			}
			tb, ok := parseTable(lines[j:k])
			if !ok {
				return nil, false
			}
			rep.summary = summaryLines(tb)
			i = k - 1
		case t == "Legend:":
			rep.legend++
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "- '") {
				i++
				rep.legend++
			}
		case i+1 < len(lines) && equalsRE.MatchString(strings.TrimSpace(lines[i+1])):
			cur = &section{name: t}
			rep.targets = append(rep.targets, cur)
			i++
			saw = true
		case cur == nil:
			// Text before any target header that is not the summary.
			return nil, false
		case totalsRE.MatchString(t):
			cur.totals = append(cur.totals, t)
		case isBox(l):
			k := i
			for k < len(lines) && isBox(lines[k]) {
				k++
			}
			tb, ok := parseTable(lines[i:k])
			if !ok || !cur.addTable(tb) {
				return nil, false
			}
			i = k - 1
		case i+1 < len(lines) && dblRE.MatchString(strings.TrimSpace(lines[i+1])):
			n := cur.parseBlock(lines, i)
			if n < 0 {
				return nil, false
			}
			i = n - 1
		default:
			cur.other = append(cur.other, t)
		}
	}
	if !saw {
		return nil, false
	}
	return rep, true
}

// parseBlock consumes one misconfiguration or secret block starting at the
// header line i and returns the index of the first line after it.
func (s *section) parseBlock(lines []string, i int) int {
	h := strings.TrimSpace(lintkit.StripANSI(lines[i]))
	var m misconf
	if g := misBlockRE.FindStringSubmatch(h); g != nil {
		m = misconf{id: g[1], sev: g[2], msg: g[3]}
	} else if g := secBlockRE.FindStringSubmatch(h); g != nil {
		m = misconf{id: "secret", sev: g[1], msg: g[2]}
	} else {
		return -1
	}
	j := i + 2 // past the ═ line
	// description up to the first ─ line
	for j < len(lines) && !sepRE.MatchString(strings.TrimSpace(lines[j])) {
		if strings.TrimSpace(lines[j]) != "" {
			s.dropped++
		}
		j++
	}
	j++ // first ─
	var loc []string
	for j < len(lines) && !sepRE.MatchString(strings.TrimSpace(lines[j])) {
		if t := strings.TrimSpace(lines[j]); t != "" {
			loc = append(loc, t)
		}
		j++
	}
	j++ // second ─
	// code excerpt up to the closing ─ line
	for j < len(lines) && !sepRE.MatchString(strings.TrimSpace(lines[j])) {
		if strings.TrimSpace(lines[j]) != "" {
			s.dropped++
		}
		j++
	}
	if j >= len(lines) {
		j = len(lines) - 1
	}
	m.loc = shortLoc(loc)
	s.issues = append(s.issues, m)
	return j + 1
}

// ---- box tables ----

type table struct {
	hdr  []string
	rows [][]string
}

const boundary = "│├┤┼┬┴┌┐└┘"

func splitCells(l string) []string {
	var cells []string
	var b strings.Builder
	for _, r := range l {
		if strings.ContainsRune(boundary, r) {
			cells = append(cells, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	cells = append(cells, b.String())
	if len(cells) < 3 {
		return nil
	}
	return cells[1 : len(cells)-1]
}

func isSepLine(l string) bool {
	if !strings.ContainsRune(l, '─') {
		return false
	}
	for _, r := range l {
		if r != '─' && r != ' ' && !strings.ContainsRune(boundary, r) {
			return false
		}
	}
	return true
}

// parseTable reads a trivy box table. A cell whose separator span above it is
// blank is a vertical merge with the previous row and inherits its value.
func parseTable(lines []string) (*table, bool) {
	if len(lines) < 3 {
		return nil, false
	}
	tb := &table{}
	var group [][]string
	var fresh []bool
	var prev []string
	first := true
	flush := func(n int) {
		if len(group) == 0 {
			return
		}
		row := make([]string, n)
		for c := 0; c < n; c++ {
			var parts []string
			for _, g := range group {
				if c < len(g) {
					if t := strings.TrimSpace(g[c]); t != "" {
						parts = append(parts, t)
					}
				}
			}
			row[c] = strings.Join(parts, " ")
			if fresh != nil && !fresh[c] && prev != nil && row[c] == "" {
				row[c] = prev[c]
			}
		}
		if first {
			tb.hdr = row
			first = false
		} else {
			tb.rows = append(tb.rows, row)
		}
		prev = row
		group = nil
	}
	n := 0
	for _, l := range lines {
		if isSepLine(l) {
			if n == 0 {
				c := splitCells(l)
				if c == nil {
					return nil, false
				}
				n = len(c)
			}
			flush(n)
			c := splitCells(l)
			if c != nil {
				if len(c) != n {
					return nil, false
				}
				fresh = make([]bool, n)
				for k, s := range c {
					fresh[k] = strings.ContainsRune(s, '─')
				}
			}
			continue
		}
		c := splitCells(l)
		if n != 0 && len(c) != n {
			return nil, false
		}
		group = append(group, c)
	}
	flush(n)
	if len(tb.hdr) == 0 {
		return nil, false
	}
	return tb, true
}

func col(hdr []string, names ...string) int {
	for i, h := range hdr {
		for _, n := range names {
			if h == n {
				return i
			}
		}
	}
	return -1
}

// addTable folds a per-target table into the section. Only the vulnerability
// table is understood; anything else makes the whole parse decline.
func (s *section) addTable(tb *table) bool {
	lib, id, sev := col(tb.hdr, "Library"), col(tb.hdr, "Vulnerability"), col(tb.hdr, "Severity")
	if lib < 0 || id < 0 || sev < 0 {
		return false
	}
	st, inst, fix := col(tb.hdr, "Status"), col(tb.hdr, "Installed Version"), col(tb.hdr, "Fixed Version")
	get := func(r []string, c int) string {
		if c < 0 {
			return ""
		}
		return r[c]
	}
	for _, r := range tb.rows {
		s.vulns = append(s.vulns, vuln{lib: r[lib], id: r[id], sev: r[sev],
			status: get(r, st), installed: get(r, inst), fixed: get(r, fix)})
	}
	return true
}

func summaryLines(tb *table) []string {
	var out, clean []string
	for _, r := range tb.rows {
		if len(r) < 2 {
			continue
		}
		var parts []string
		allZero := true
		for c := 2; c < len(r); c++ {
			v := strings.TrimSpace(r[c])
			if v == "-" || v == "" {
				continue
			}
			if v != "0" {
				allZero = false
			}
			parts = append(parts, strings.ToLower(tb.hdr[c])+"="+v)
		}
		if r[0] == "-" && len(parts) == 0 {
			out = append(out, "no targets scanned")
			continue
		}
		if allZero {
			clean = append(clean, r[0])
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s): %s", r[0], r[1], strings.Join(parts, " ")))
	}
	if len(clean) > 0 {
		out = append(out, fmt.Sprintf("clean (%d): %s", len(clean), strings.Join(clean, ", ")))
	}
	return out
}

func isHigh(sev string) bool { return sev == "CRITICAL" || sev == "HIGH" }

func (r *report) render() string {
	var b strings.Builder
	b.WriteString("trivy:")
	for _, l := range r.summary {
		b.WriteString("\n  " + l)
	}
	for _, t := range r.targets {
		b.WriteString("\n" + t.name)
		for _, l := range t.totals {
			b.WriteString("\n" + l)
		}
		t.renderVulns(&b)
		t.renderIssues(&b)
		for _, o := range t.other {
			b.WriteString("\n" + o)
		}
		if t.dropped > 0 {
			fmt.Fprintf(&b, "\n…+%d description/code lines dropped", t.dropped)
		}
	}
	return b.String()
}

func (t *section) renderVulns(b *strings.Builder) {
	type pk struct{ lib, inst string }
	var order []pk
	by := map[pk][]vuln{}
	rest := map[pk]map[string]int{}
	var restOrder []pk
	for _, v := range t.vulns {
		k := pk{v.lib, v.installed}
		if isHigh(v.sev) {
			if _, ok := by[k]; !ok {
				order = append(order, k)
			}
			by[k] = append(by[k], v)
			continue
		}
		if rest[k] == nil {
			rest[k] = map[string]int{}
			restOrder = append(restOrder, k)
		}
		rest[k][v.sev]++
	}
	for _, k := range order {
		b.WriteString("\n  " + k.lib)
		if k.inst != "" {
			b.WriteString(" " + k.inst)
		}
		for _, v := range by[k] {
			b.WriteString("\n    " + v.id + " " + v.sev)
			switch {
			case v.fixed != "":
				b.WriteString(" -> " + v.fixed)
			case v.status != "":
				b.WriteString(" (" + v.status + ")")
			default:
				b.WriteString(" (no fix)")
			}
		}
	}
	if len(restOrder) > 0 {
		var parts []string
		total := 0
		for _, k := range restOrder {
			var sevs []string
			for _, s := range []string{"MEDIUM", "LOW", "UNKNOWN"} {
				if n := rest[k][s]; n > 0 {
					sevs = append(sevs, fmt.Sprintf("%s×%d", strings.ToLower(s[:1]), n))
					total += n
				}
			}
			parts = append(parts, strings.TrimSpace(k.lib+" "+k.inst)+" "+strings.Join(sevs, ","))
		}
		fmt.Fprintf(b, "\n  …+%d MEDIUM/LOW/UNKNOWN (m=medium l=low u=unknown): %s", total, strings.Join(parts, "; "))
	}
}

func (t *section) renderIssues(b *strings.Builder) {
	hist := map[string]int{}
	total := 0
	for _, m := range t.issues {
		if m.id == "secret" || isHigh(m.sev) {
			b.WriteString("\n  " + m.sev + " " + m.id + " " + lintkit.Clip(m.msg))
			if m.loc != "" {
				b.WriteString(" @ " + m.loc)
			}
			continue
		}
		hist[m.id]++
		total++
	}
	if total > 0 {
		type kv struct {
			k string
			n int
		}
		var kvs []kv
		for k, n := range hist {
			kvs = append(kvs, kv{k, n})
		}
		sort.Slice(kvs, func(i, j int) bool {
			if kvs[i].n != kvs[j].n {
				return kvs[i].n > kvs[j].n
			}
			return kvs[i].k < kvs[j].k
		})
		parts := make([]string, len(kvs))
		for i, e := range kvs {
			parts[i] = fmt.Sprintf("%s×%d", e.k, e.n)
		}
		fmt.Fprintf(b, "\n  …+%d MEDIUM/LOW/UNKNOWN checks: %s", total, strings.Join(parts, " "))
	}
}

var resourceRE = regexp.MustCompile(`\(([^()]+)\)$`)

// shortLoc keeps the primary location and, for terraform "via" chains, only
// the owning resource name from the last hop.
func shortLoc(loc []string) string {
	if len(loc) == 0 {
		return ""
	}
	var via []string
	for _, l := range loc[1:] {
		if !strings.HasPrefix(l, "via ") {
			return strings.Join(loc, "; ")
		}
		via = append(via, l)
	}
	if len(via) == 0 {
		return loc[0]
	}
	if m := resourceRE.FindStringSubmatch(via[len(via)-1]); m != nil {
		return loc[0] + " (" + m[1] + ")"
	}
	return loc[0]
}
