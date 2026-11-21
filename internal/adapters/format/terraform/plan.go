package terraform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// noiseRules are lines that carry no decision-relevant signal in plan/apply
// output.
var noiseRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"refresh", regexp.MustCompile(`: Refreshing state\.\.\.`)},
	{"read", regexp.MustCompile(`: (Reading\.\.\.$|Read complete after )`)},
	{"lock", regexp.MustCompile(`^(Acquiring|Releasing) state lock`)},
	{"progress", regexp.MustCompile(`: (Creating|Modifying|Destroying)\.\.\.$|: Still (creating|modifying|destroying|reading|refreshing)\.\.\.`)},
	{"provider", regexp.MustCompile(`^- (Finding|Installing|Reusing previous) `)},
}

// doneRE matches per-resource completion lines. A successful run's summary line
// carries the totals, so the aggressive tier drops them only on exit 0 — after a
// failure they say which resources did get applied.
var doneRE = regexp.MustCompile(`: (Creation|Modifications|Destruction) complete after `)

// computedRE matches an attribute of a created resource whose value is not
// known until apply (or a stale value replaced by one): schema churn with no
// decision content. Lines carrying "forces replacement" never match.
var computedRE = regexp.MustCompile(`^[+~]\s+[^\s=]+\s+= (.* -> )?\(known after apply\)$`)

// legendRE matches the plan preamble, dropped by the aggressive tier only.
var legendRE = regexp.MustCompile(`^((Terraform|OpenTofu) used the selected providers|plan\. Resource actions are indicated|Resource actions are indicated with the following symbols:|(Terraform|OpenTofu) will perform the following actions:|\s*(\+|~|-|-/\+|\+/-|<=) (create|update in-place|destroy|read|destroy and then create replacement|create replacement and then destroy)( \(data resources\))?$)`)

var (
	headerRE   = regexp.MustCompile(`^#\s+\S.*?\s+(will be |must be |has moved|has changed|has been |is tainted|will no longer)`)
	sigilRE    = regexp.MustCompile(`^(\+|-|~|-/\+|\+/-|<=)\s`)
	heredocRE  = regexp.MustCompile(`<<[-~]?([A-Za-z_][A-Za-z0-9_]*)\s*$`)
	summaryRE  = regexp.MustCompile(`^(Plan:|No changes\.|Apply complete!|Destroy complete!|Changes to Outputs:|Error:|Warning:)`)
	knownRE    = regexp.MustCompile(`^(Plan:|No changes\.|Apply complete!|Destroy complete!|Terraform will perform|OpenTofu will perform)`)
	boxPrefix  = regexp.MustCompile(`^[│╷╵]\s?`)
	diagStartR = regexp.MustCompile(`^(Error|Warning):`)
)

const (
	capDefault  = 60
	capLongPlan = 15
	capDestroy  = 6
	longPlan    = 300
)

type scanner struct {
	aggressive bool
	exitOK     bool
	attrCap    int
	blockCap   int

	out        []string
	counts     map[string]int
	recognized bool

	inDiag bool

	inBlock    bool
	seenRes    bool
	closeInd   int
	kept       int
	capDropped int
	unchanged  int
	computed   int
	dropInd    int // indent of an unchanged container being dropped; -1 none
	heredoc    string
	heredocKp  bool
}

func newScanner(aggressive, exitOK bool, total int) *scanner {
	s := &scanner{aggressive: aggressive, exitOK: exitOK, attrCap: capDefault, counts: map[string]int{}, dropInd: -1}
	if total > longPlan {
		s.attrCap = capLongPlan
	}
	return s
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func renderPlan(in format.Input, aggressive bool) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("terraform: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("terraform: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := splitLines(string(stdout))
	s := newScanner(aggressive, in.ExitCode == 0, len(lines))
	for _, raw := range lines {
		s.line(strings.TrimRight(raw, "\r"))
	}
	s.endBlock()
	marker := s.marker()
	if !s.recognized || marker == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	b.WriteString(marker)
	for _, l := range s.out {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fold := false
	if len(stderr) > 0 {
		b.Write(stderr)
		if stderr[len(stderr)-1] != '\n' {
			b.WriteByte('\n')
		}
		fold = true
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: true}, nil
}

// marker lists everything dropped, as one line at the top of the body.
func (s *scanner) marker() string {
	var parts []string
	n := 0
	for _, k := range []string{"refresh", "read", "lock", "progress", "done", "provider", "legend", "unchanged", "attributes", "blank"} {
		c := s.counts[k]
		if c == 0 {
			continue
		}
		n += c
		name := k
		switch k {
		case "attributes":
			name = "capped attributes"
		case "done":
			name = "completed"
		case "computed":
			name = "known-after-apply attributes"
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", name, c))
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("…+%d lines (%s)\n", n, strings.Join(parts, ", "))
}

func (s *scanner) keep(l string)   { s.out = append(s.out, l) }
func (s *scanner) drop(cat string) { s.counts[cat]++ }

func (s *scanner) line(raw string) {
	plain := ansiRE.ReplaceAllString(raw, "")
	trimmed := strings.TrimSpace(plain)
	unboxed := strings.TrimSpace(boxPrefix.ReplaceAllString(trimmed, ""))

	// Error and Warning blocks are kept verbatim, including their blank lines.
	if s.inDiag {
		s.keep(raw)
		if strings.HasPrefix(trimmed, "╵") {
			s.inDiag = false
		}
		return
	}
	if diagStartR.MatchString(unboxed) {
		s.endBlock()
		s.recognized = true
		s.inDiag = true
		s.keep(raw)
		return
	}

	// A true return means the line ended the block and is a top-level line.
	if s.inBlock && !s.blockLine(raw, plain, trimmed) {
		return
	}

	for _, r := range noiseRules {
		if r.re.MatchString(plain) {
			s.recognized = true
			s.drop(r.name)
			return
		}
	}
	if s.aggressive && s.exitOK && doneRE.MatchString(plain) {
		s.recognized = true
		s.drop("done")
		return
	}
	if s.aggressive && legendRE.MatchString(plain) {
		s.recognized = true
		s.drop("legend")
		return
	}
	if trimmed == "" {
		s.drop("blank")
		return
	}
	if headerRE.MatchString(trimmed) {
		s.recognized = true
		s.startBlock(trimmed)
		s.keep(raw)
		return
	}
	if knownRE.MatchString(trimmed) {
		s.recognized = true
	}
	s.keep(raw)
}

func (s *scanner) startBlock(header string) {
	if !s.aggressive {
		return
	}
	s.inBlock = true
	s.seenRes = false
	s.kept, s.capDropped, s.unchanged, s.computed = 0, 0, 0, 0
	s.dropInd = -1
	s.heredoc = ""
	// A destroyed resource lists every attribute it had; the address in the
	// header is the decision, so only the first few attributes are kept.
	s.blockCap = s.attrCap
	if strings.Contains(header, "will be destroyed") && s.blockCap > capDestroy {
		s.blockCap = capDestroy
	}
}

// endBlock flushes the per-resource elision markers.
func (s *scanner) endBlock() {
	if !s.inBlock {
		return
	}
	s.inBlock = false
	s.flushMarkers()
}

func (s *scanner) flushMarkers() {
	var parts []string
	if s.capDropped > 0 {
		parts = append(parts, fmt.Sprintf("…+%d attributes", s.capDropped))
		s.counts["attributes"] += s.capDropped
	}
	if s.unchanged > 0 {
		parts = append(parts, fmt.Sprintf("…+%d unchanged", s.unchanged))
		s.counts["unchanged"] += s.unchanged
	}
	if s.computed > 0 {
		parts = append(parts, fmt.Sprintf("…+%d known after apply", s.computed))
		s.counts["computed"] += s.computed
	}
	if len(parts) > 0 {
		s.keep("      " + strings.Join(parts, " "))
	}
	s.capDropped, s.unchanged, s.computed = 0, 0, 0
}

// blockLine consumes one line inside a resource block. It returns true when
// the line is NOT part of the block (the block ended before it) so the caller
// handles it as a top-level line.
func (s *scanner) blockLine(raw, plain, trimmed string) bool {
	ind := indentOf(plain)

	if s.heredoc != "" {
		if s.heredocKp {
			s.attr(raw, trimmed)
		} else {
			s.unchanged++
		}
		// A modified heredoc closes as "EOT -> (known after apply)".
		if trimmed == s.heredoc || strings.HasPrefix(trimmed, s.heredoc+" -> ") {
			s.heredoc = ""
		}
		return false
	}

	if s.seenRes && trimmed == "}" && ind == s.closeInd {
		s.flushMarkers()
		s.inBlock = false
		s.keep(raw)
		return false
	}
	// Defensive exits: anything that cannot belong to a resource body.
	if s.seenRes && (summaryRE.MatchString(trimmed) || headerRE.MatchString(trimmed) || strings.HasPrefix(trimmed, "╷")) {
		s.endBlock()
		return true
	}

	if s.dropInd >= 0 {
		s.unchanged++
		if (strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]")) && ind == s.dropInd {
			s.dropInd = -1
		}
		return false
	}
	if trimmed == "" {
		s.drop("blank")
		return false
	}
	if !s.seenRes {
		s.seenRes = true
		s.closeInd = ind
		// With a sigil the closing brace aligns with the text after it.
		if loc := sigilRE.FindStringIndex(trimmed); loc != nil {
			s.closeInd += loc[1]
		}
		s.keep(raw)
		return false
	}

	if computedRE.MatchString(trimmed) {
		s.computed++
		return false
	}

	changing := sigilRE.MatchString(trimmed) || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]") || strings.HasPrefix(trimmed, ")") ||
		strings.Contains(trimmed, "forces replacement")
	if !changing {
		s.unchanged++
		if m := heredocRE.FindStringSubmatch(trimmed); m != nil {
			s.heredoc, s.heredocKp = m[1], false
		} else if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "[") || strings.HasSuffix(trimmed, "(") {
			s.dropInd = ind
		}
		return false
	}
	kept := s.attr(raw, trimmed)
	if m := heredocRE.FindStringSubmatch(trimmed); m != nil {
		s.heredoc, s.heredocKp = m[1], kept
	}
	return false
}

// attr emits a changing attribute line subject to the per-resource cap and
// reports whether it was kept. Replacement reasons always survive the cap.
func (s *scanner) attr(raw, trimmed string) bool {
	if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "unchanged") {
		s.keep(raw)
		return true
	}
	s.kept++
	if s.kept > s.blockCap && !strings.Contains(trimmed, "forces replacement") {
		s.capDropped++
		return false
	}
	s.keep(raw)
	return true
}
