// Package govulncheck formats the default text report of `govulncheck`. Each
// finding keeps its vulnerability ID, module, found-in and fixed-in versions,
// title and call-path entries; the "More info" URLs (derivable from the ID),
// the scan banner and the verbose-mode hint are counted instead of printed.
// JSON, SARIF and OpenVEX output is machine output and is never touched.
package govulncheck

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxTraces is how many call-path entries are listed per module record.
const maxTraces = 3

var (
	sectionRE  = regexp.MustCompile(`^=== (Symbol|Package|Module) Results ===$`)
	vulnRE     = regexp.MustCompile(`^Vulnerability #\d+: (\S+)`)
	moreInfoRE = regexp.MustCompile(`^\s+More info: `)
	moduleRE   = regexp.MustCompile(`^\s+Module: (.+)$`)
	foundRE    = regexp.MustCompile(`^\s+Found in: (.+)$`)
	fixedRE    = regexp.MustCompile(`^\s+Fixed in: (.+)$`)
	platRE     = regexp.MustCompile(`^\s+Platforms: (.+)$`)
	traceHdrRE = regexp.MustCompile(`^\s+(Example traces found|Vulnerable symbols found):$`)
	traceRE    = regexp.MustCompile(`^(\s+)#\d+: `)
	bannerRE   = regexp.MustCompile(`^(Scanning your |Fetching vulnerabilities|Checking the |The package pattern matched)`)
	verboseRE  = regexp.MustCompile(`^Use '-show verbose' for more details\.$`)
)

var sectionTag = map[string]string{"Symbol": "called", "Package": "imported", "Module": "required"}

// Formatter renders govulncheck text output.
type Formatter struct{}

// New constructs a govulncheck Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every govulncheck invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "govulncheck"} }

// machineOutput reports flags that select JSON/SARIF/OpenVEX output or a
// non-scan action.
func machineOutput(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := strings.TrimLeft(argv[i], "-")
		k, v, hasVal := strings.Cut(a, "=")
		switch k {
		case "json", "version", "h", "help":
			return true
		case "format":
			if !hasVal && i+1 < len(argv) {
				v = argv[i+1]
			}
			if v != "text" {
				return true
			}
		}
	}
	return false
}

type modRec struct {
	name, found, fixed, platforms string
	traces                        [][]string
}

type vuln struct {
	id, tag string
	title   []string
	mods    []*modRec
}

type state struct {
	out    []string
	urls   int
	banner int
	hints  int
	traces int // entries beyond maxTraces
}

func (s *state) flush(v *vuln) {
	if v == nil {
		return
	}
	mods := v.mods
	if len(mods) == 0 {
		mods = []*modRec{{}}
	}
	for i, m := range mods {
		head := v.id
		if m.found != "" {
			head += " " + m.found
		} else if m.name != "" {
			head += " " + m.name
		}
		switch {
		case m.fixed == "" || strings.EqualFold(m.fixed, "N/A"):
			head += " (no fix)"
		default:
			head += " -> " + strings.TrimPrefix(m.fixed, m.name+"@")
		}
		head += " [" + v.tag + "]"
		if n := len(m.traces); n > 0 {
			head += fmt.Sprintf(" %d trace(s)", n)
		}
		if m.platforms != "" {
			head += " platforms: " + m.platforms
		}
		s.out = append(s.out, head)
		if i == 0 && len(v.title) > 0 {
			s.out = append(s.out, "  "+strings.Join(v.title, " "))
		}
		for j, tr := range m.traces {
			if j == maxTraces {
				s.out = append(s.out, fmt.Sprintf("  …+%d more traces", len(m.traces)-maxTraces))
				s.traces += len(m.traces) - maxTraces
				break
			}
			s.out = append(s.out, tr...)
		}
	}
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// Aggressive renders the report as one block per finding.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	o, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("govulncheck: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("govulncheck: reading stderr: %w", err)
	}
	var s state
	var cur *vuln
	var mod *modRec
	tag := ""
	inBanner := false
	trailing := false
	found := 0
	for _, l := range splitLines(string(o)) {
		l = strings.TrimRight(l, "\r")
		switch {
		case sectionRE.MatchString(l):
			s.flush(cur)
			cur, mod, inBanner = nil, nil, false
			tag = sectionTag[sectionRE.FindStringSubmatch(l)[1]]
			continue
		case vulnRE.MatchString(l):
			s.flush(cur)
			cur = &vuln{id: vulnRE.FindStringSubmatch(l)[1], tag: tag}
			mod = nil
			found++
			continue
		case trailing:
			if verboseRE.MatchString(l) {
				s.hints++
				continue
			}
			if strings.TrimSpace(l) != "" {
				s.out = append(s.out, l)
			}
			continue
		case cur == nil:
			switch {
			case bannerRE.MatchString(l):
				s.banner++
				inBanner = strings.HasPrefix(l, "The package pattern")
			case inBanner && strings.HasPrefix(l, "  "):
				s.banner++
			case strings.HasPrefix(l, "Govulncheck scanned"):
				inBanner = true
				s.out = append(s.out, strings.TrimSuffix(l, ":"))
			case strings.TrimSpace(l) == "":
			default:
				if strings.HasPrefix(l, "Your code") || strings.HasPrefix(l, "No vulnerabilities") || strings.HasPrefix(l, "Your binary") {
					trailing = true
				}
				s.out = append(s.out, l)
			}
			continue
		}
		// Inside a vulnerability block.
		switch {
		case strings.TrimSpace(l) == "":
			s.flush(cur)
			cur, mod = nil, nil
		case moreInfoRE.MatchString(l):
			s.urls++
		case moduleRE.MatchString(l):
			mod = &modRec{name: moduleRE.FindStringSubmatch(l)[1]}
			cur.mods = append(cur.mods, mod)
		case mod != nil && foundRE.MatchString(l):
			mod.found = foundRE.FindStringSubmatch(l)[1]
		case mod != nil && fixedRE.MatchString(l):
			mod.fixed = fixedRE.FindStringSubmatch(l)[1]
		case mod != nil && platRE.MatchString(l):
			mod.platforms = platRE.FindStringSubmatch(l)[1]
		case traceHdrRE.MatchString(l):
		case mod != nil && traceRE.MatchString(l):
			mod.traces = append(mod.traces, []string{"  " + strings.TrimSpace(l)})
		case mod != nil && len(mod.traces) > 0 && strings.HasPrefix(l, "        "):
			last := &mod.traces[len(mod.traces)-1]
			*last = append(*last, "    "+strings.TrimSpace(l))
		case mod == nil:
			cur.title = append(cur.title, strings.TrimSpace(l))
		default:
			// An unrecognised line inside a block is kept as its own line.
			cur.title = append(cur.title, strings.TrimSpace(l))
		}
	}
	s.flush(cur)
	if found == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	s.out = append(s.out, splitLines(string(e))...)
	var parts []string
	n := 0
	for _, p := range []struct {
		name string
		c    int
	}{{"more-info URLs", s.urls}, {"scan banner", s.banner}, {"verbose hint", s.hints}, {"traces", s.traces}} {
		if p.c > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", p.name, p.c))
			n += p.c
		}
	}
	body := strings.Join(s.out, "\n") + "\n"
	if n > 0 {
		body += fmt.Sprintf("…+%d lines (%s; details: https://pkg.go.dev/vuln/<ID>)\n", n, strings.Join(parts, ", "))
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: n > 0}, nil
}

var noiseRE = regexp.MustCompile(`^(Fetching vulnerabilities|Checking the |Scanning your )`)

// Relaxed drops only the scan banner lines and the verbose hint.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	o, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("govulncheck: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("govulncheck: reading stderr: %w", err)
	}
	var kept []string
	dropped := 0
	for _, l := range splitLines(string(o)) {
		if noiseRE.MatchString(l) || verboseRE.MatchString(l) {
			dropped++
			continue
		}
		kept = append(kept, l)
	}
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, splitLines(string(e))...)
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d lines (banner/hint)\n", dropped)
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true}, nil
}
