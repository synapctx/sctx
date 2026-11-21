package cargo

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	// progressRE matches cargo's status column. "Installing <path>" (the
	// destination) is deliberately not progress; installingNameRE covers the
	// name-and-version form that precedes the build.
	progressRE = regexp.MustCompile(`^\s*(Compiling|Checking|Downloaded|Downloading|Fresh|Running|Doc-tests|Updating|Locking|Blocking|Adding|Unpacking|Documenting|Dirty|Installed|Removing|Removed|Packaging|Packaged|Verifying|Archiving|Creating|Vendoring|Generating) `)
	// installingNameRE: "Installing demo v0.1.0 (...)" starts an install; the
	// destination line "Installing /abs/bin/demo" is the result and is kept.
	installingNameRE = regexp.MustCompile(`^\s*Installing [^/\s]+ v\d`)
	finishedRE       = regexp.MustCompile("^\\s*Finished `?([\\w-]+)`? (?:profile )?\\[[^\\]]*\\] target\\(s\\) in (.+)$")
	testOKRE         = regexp.MustCompile(`^test .* \.\.\. (ok|ignored(, .*)?)$`)
	runningRE        = regexp.MustCompile(`^running \d+ tests?$`)
	resultRE         = regexp.MustCompile(`^test result: (ok|FAILED)\. (\d+) passed; (\d+) failed; (\d+) ignored`)
	failedRE         = regexp.MustCompile(`^test (.*) \.\.\. FAILED$`)
	failedQuietRE    = regexp.MustCompile(`^(\S.*) --- FAILED$`)
	diagRE           = regexp.MustCompile(`^(error|warning)(\[\w+\])?: `)
	failHdrRE        = regexp.MustCompile(`^---- (.*) stdout ----$`)
	frameRE          = regexp.MustCompile(`^\s+(\d+: |at )`)
	lintRE           = regexp.MustCompile(`rust-clippy/[^/]+/index\.html#(\w+)`)
	lintNoteRE       = regexp.MustCompile("`#\\[(?:warn|deny|forbid)\\(([\\w:]+)\\)\\]`")
	locRE            = regexp.MustCompile(`^\s*--> (.+)$`)
	dotsRE           = regexp.MustCompile(`^([.FiE]{2,}|[.FiE]+ +\d+/\d+)$`)
	threadIDRE       = regexp.MustCompile(`^(thread '.*') \(\d+\)( panicked at .*)$`)
	hintRE           = regexp.MustCompile("^note: (run with `RUST_BACKTRACE=1`|Some details are omitted, run with `RUST_BACKTRACE=full`)")
)

type scan struct {
	out     []string
	counts  map[string]int
	known   bool
	passed  int
	failed  int
	ignored int
	results int
	names   []string
	nameSet map[string]bool
	// blockNames are failing tests whose output block was kept, so listing
	// them again would only repeat the block header.
	blockNames map[string]bool
	testMode   bool
	// compactLintErrors compacts clippy lints promoted to errors (-D warnings).
	compactLintErrors bool
}

func newScan(testMode bool) *scan {
	return &scan{counts: map[string]int{}, nameSet: map[string]bool{}, blockNames: map[string]bool{}, testMode: testMode}
}

func (s *scan) addName(n string) {
	if !s.nameSet[n] {
		s.nameSet[n] = true
		s.names = append(s.names, n)
	}
}

// emitDiag keeps errors byte-exact and compacts warnings that carry a source
// location to one line: the snippet, ruler and suggestion lines are counted
// in the elision marker instead of being repeated.
func (s *scan) emitDiag(cur []string) {
	if len(cur) == 0 {
		return
	}
	for len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1]) == "" {
		cur = cur[:len(cur)-1]
	}
	head := strings.TrimSpace(cur[0])
	isWarn := strings.HasPrefix(head, "warning: ") || strings.HasPrefix(head, "warning[")
	// With -D warnings clippy lints surface as errors; they carry the same
	// message, location and lint name as the warning form, so they compact too.
	lintErr := false
	if !isWarn && s.compactLintErrors && strings.HasPrefix(head, "error: ") {
		for _, l := range cur[1:] {
			if lintRE.MatchString(l) {
				lintErr = true
				break
			}
		}
	}
	if !isWarn && !lintErr {
		s.out = append(s.out, cur...)
		if len(cur) > 1 {
			s.out = append(s.out, "")
		}
		return
	}
	loc := ""
	for _, l := range cur[1:] {
		if m := locRE.FindStringSubmatch(l); m != nil {
			loc = m[1]
			break
		}
	}
	if loc == "" {
		s.out = append(s.out, cur...)
		if len(cur) > 1 {
			s.out = append(s.out, "")
		}
		return
	}
	lint := ""
	for _, l := range cur[1:] {
		if m := lintNoteRE.FindStringSubmatch(l); m != nil {
			lint = m[1]
			break
		}
	}
	if lint == "" {
		for _, l := range cur[1:] {
			if m := lintRE.FindStringSubmatch(l); m != nil {
				lint = "clippy::" + m[1]
				break
			}
		}
	}
	label, msg := "warning", strings.TrimPrefix(head, "warning: ")
	if lintErr {
		label, msg = "error", strings.TrimPrefix(head, "error: ")
	}
	line := loc + ": " + label + ": " + msg
	if lint != "" {
		line += " [" + lint + "]"
	}
	s.out = append(s.out, line)
	s.counts["diag detail"] += len(cur) - 1
}

// feed scans one stream. Unrecognised lines are kept verbatim.
func (s *scan) feed(lines []string) {
	const (
		stNone = iota
		stAfterFailures
		stBlocks
		stNames
		stDiag
	)
	st := stNone
	var cur []string
	var blockLines []string
	inBacktrace := false
	droppedFrames := 0
	pendingFrame := false // last backtrace frame header awaiting its "at" line
	keepFrame := false

	flushBlock := func() {
		if len(blockLines) == 0 {
			return
		}
		for len(blockLines) > 0 && strings.TrimSpace(blockLines[len(blockLines)-1]) == "" {
			blockLines = blockLines[:len(blockLines)-1]
		}
		s.out = append(s.out, blockLines...)
		if droppedFrames > 0 {
			s.out = append(s.out, fmt.Sprintf("    …+%d backtrace frames", droppedFrames))
			s.counts["backtrace"] += droppedFrames
		}
		blockLines, droppedFrames, inBacktrace, pendingFrame = nil, 0, false, false
	}
	flushDiag := func() {
		s.emitDiag(cur)
		cur = nil
	}

	for _, raw := range lines {
		l := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(l)

		if st == stDiag {
			if t == "" {
				flushDiag()
				st = stNone
				continue
			}
			// A one-line summary such as "warning: `x` generated N warnings" is
			// followed directly by progress or the next diagnostic, not a blank.
			if !progressRE.MatchString(l) && !finishedRE.MatchString(l) && !strings.HasPrefix(t, "Finished ") && !diagRE.MatchString(l) {
				cur = append(cur, l)
				continue
			}
			flushDiag()
			st = stNone
		}

		switch st {
		case stAfterFailures:
			switch {
			case t == "":
				continue
			case failHdrRE.MatchString(t):
				st = stBlocks
			case strings.HasPrefix(l, "    "):
				st = stNames
			default:
				st = stNone
			}
		case stBlocks:
			if t == "failures:" {
				flushBlock()
				st = stNames
				continue
			}
			if failHdrRE.MatchString(t) {
				flushBlock()
			} else if resultRE.MatchString(t) {
				flushBlock()
				st = stNone
			}
		}
		if st == stNames {
			if strings.HasPrefix(l, "    ") && t != "" {
				s.addName(t)
				s.counts["list"]++
				continue
			}
			if t != "" && t != "failures:" {
				st = stNone
			} else {
				if t == "" {
					st = stNone
				}
				continue
			}
		}
		if st == stBlocks {
			if m := failHdrRE.FindStringSubmatch(t); m != nil {
				s.blockNames[m[1]] = true
				s.addName(m[1])
				blockLines = append(blockLines, l)
				continue
			}
			if hintRE.MatchString(t) {
				s.counts["backtrace hint"]++
				continue
			}
			if m := threadIDRE.FindStringSubmatch(l); m != nil {
				l = m[1] + m[2]
			}
			if t == "" && len(blockLines) > 0 && failHdrRE.MatchString(strings.TrimSpace(blockLines[len(blockLines)-1])) {
				continue // blank line directly after the header
			}
			if inBacktrace && frameRE.MatchString(l) {
				// Keep frames in the user's crate (their location is
				// workspace-relative); count the toolchain's own.
				if strings.Contains(l, ": ") && !strings.HasPrefix(strings.TrimSpace(l), "at ") {
					pendingFrame, keepFrame = true, false
					frameHead := l
					if strings.Contains(l, "core::") || strings.Contains(l, "std::") || strings.Contains(l, "alloc::") || strings.Contains(l, "__rustc") || strings.Contains(l, "test::") {
						droppedFrames++
						continue
					}
					blockLines = append(blockLines, frameHead)
					keepFrame = true
					continue
				}
				if pendingFrame && keepFrame {
					blockLines = append(blockLines, l)
					pendingFrame, keepFrame = false, false
					continue
				}
				continue
			}
			if t == "stack backtrace:" {
				inBacktrace = true
			}
			blockLines = append(blockLines, l)
			continue
		}

		switch {
		case t == "failures:" && s.testMode:
			s.known = true
			st = stAfterFailures
		case installingNameRE.MatchString(l):
			s.known = true
			s.counts["installing"]++
		case progressRE.MatchString(l):
			s.known = true
			s.counts[strings.ToLower(strings.Fields(l)[0])]++
		case s.testMode && testOKRE.MatchString(t):
			s.known = true
			if strings.HasSuffix(t, "ok") {
				s.counts["test ok"]++
			} else {
				s.counts["test ignored"]++
			}
		case s.testMode && runningRE.MatchString(t):
			s.known = true
			s.counts["test hdr"]++
		case s.testMode && dotsRE.MatchString(t):
			s.counts["progress"]++
		case t == "":
			// blank formatting lines are not content
		case s.testMode && resultRE.MatchString(t):
			s.known = true
			m := resultRE.FindStringSubmatch(t)
			var p, f, i int
			fmt.Sscan(m[2], &p)
			fmt.Sscan(m[3], &f)
			fmt.Sscan(m[4], &i)
			s.passed += p
			s.failed += f
			s.ignored += i
			s.results++
		case s.testMode && failedRE.MatchString(t):
			s.known = true
			s.addName(failedRE.FindStringSubmatch(t)[1])
		case s.testMode && failedQuietRE.MatchString(t):
			s.known = true
			s.addName(failedQuietRE.FindStringSubmatch(t)[1])
		case diagRE.MatchString(l):
			s.known = true
			cur = []string{l}
			st = stDiag
		case finishedRE.MatchString(l):
			s.known = true
			m := finishedRE.FindStringSubmatch(l)
			s.out = append(s.out, "Finished "+m[1]+" in "+m[2])
		case strings.HasPrefix(t, "Finished "):
			s.known = true
			s.out = append(s.out, l)
		default:
			s.out = append(s.out, l)
		}
	}
	flushBlock()
	flushDiag()
}

// order of categories in the elision marker.
var markerOrder = []string{"compiling", "checking", "documenting", "downloaded", "downloading", "fresh", "running", "doc-tests", "updating", "locking", "blocking", "adding", "unpacking", "dirty", "installing", "installed", "packaging", "packaged", "verifying", "archiving", "creating", "vendoring", "generating", "removing", "removed", "test ok", "test ignored", "test hdr", "progress", "list", "backtrace", "backtrace hint", "diag detail"}

func (s *scan) marker() string {
	n := 0
	var parts []string
	for _, k := range markerOrder {
		if c := s.counts[k]; c > 0 {
			n += c
			parts = append(parts, fmt.Sprintf("%s ×%d", k, c))
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("…+%d lines (%s)", n, strings.Join(parts, ", "))
}

func hasANSI(b []byte) bool {
	for _, c := range b {
		if c == 0x1b {
			return true
		}
	}
	return false
}

func readStreams(in format.Input) (stdout, stderr []byte, err error) {
	stdout, err = readAll(in.Stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("cargo: reading stdout: %w", err)
	}
	stderr, err = readAll(in.Stderr)
	if err != nil {
		return nil, nil, fmt.Errorf("cargo: reading stderr: %w", err)
	}
	return stdout, stderr, nil
}

func render(in format.Input, sub string) (format.Rendered, error) {
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if sub == "fmt" {
		return renderFmt(stdout, stderr)
	}
	// Colour escapes defeat line recognition; leave those bytes alone.
	if hasANSI(stdout) || hasANSI(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if sub == "nextest" {
		return renderNextest(stdout, stderr)
	}

	s := newScan(sub == "test")
	s.compactLintErrors = sub == "clippy"
	s.feed(splitLines(string(stderr)))
	errOut := s.out
	s.out = nil
	var outOut []string
	if sub == "run" {
		// stdout is the program's own output: never interpret it.
		outOut = splitLines(string(stdout))
	} else {
		s.feed(splitLines(string(stdout)))
		outOut = s.out
	}
	if !s.known {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	if sub == "test" && s.results > 0 {
		fmt.Fprintf(&b, "cargo test: %d passed, %d failed", s.passed, s.failed)
		if s.ignored > 0 {
			fmt.Fprintf(&b, ", %d ignored", s.ignored)
		}
		b.WriteString("\n")
	}
	for _, n := range s.names {
		if !s.blockNames[n] {
			fmt.Fprintf(&b, "FAILED %s\n", n)
		}
	}
	// Compiler diagnostics (stderr) first, then the failing-test blocks.
	for _, l := range errOut {
		b.WriteString(l + "\n")
	}
	for _, l := range outOut {
		b.WriteString(l + "\n")
	}
	m := s.marker()
	if m != "" {
		b.WriteString(m + "\n")
	}
	body := strings.Trim(b.String(), "\n") + "\n"
	if strings.TrimSpace(body) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: m != ""}, nil
}

func renderRelaxed(in format.Input, sub string) (format.Rendered, error) {
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if hasANSI(stdout) || hasANSI(stderr) || sub == "fmt" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	counts := map[string]int{}
	var out []string
	filter := func(text string, interpret bool) {
		for _, l := range splitLines(text) {
			t := strings.TrimSpace(l)
			switch {
			case !interpret:
				out = append(out, l)
			case progressRE.MatchString(l) || installingNameRE.MatchString(l):
				counts[strings.ToLower(strings.Fields(l)[0])]++
			case sub == "test" && testOKRE.MatchString(t):
				counts["test ok"]++
			case sub == "nextest" && nextestPassRE.MatchString(l):
				counts["test ok"]++
			case t == "":
			default:
				out = append(out, l)
			}
		}
	}
	filter(string(stderr), true)
	filter(string(stdout), sub != "run")
	s := &scan{counts: counts}
	m := s.marker()
	if m == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := strings.Join(out, "\n")
	if body != "" {
		body += "\n"
	}
	body += m + "\n"
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true}, nil
}
