package dotnet

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	diagRE      = regexp.MustCompile(`^(.*?)(?:\((\d+),(\d+)(?:,\d+,\d+)?\))? ?: (error|warning) ([A-Za-z]+[0-9]*): (.*?)(?: \[([^\]]+)\])?\s*$`)
	arrowRE     = regexp.MustCompile(`^\s+(\S+) -> (.+)$`)
	buildRE     = regexp.MustCompile(`^Build (succeeded|FAILED)\.\s*$`)
	countRE     = regexp.MustCompile(`^\s+(\d+) (Warning|Error)\(s\)\s*$`)
	elapsedRE   = regexp.MustCompile(`^Time Elapsed (\S+)\s*$`)
	testStartRE = regexp.MustCompile(`^  (Failed|Passed|Skipped) (.+?)(?: \[[^\]]*\])?$`)
	summaryRE   = regexp.MustCompile(`^(Passed|Failed)!\s+- (.*)$`)
	frameRE     = regexp.MustCompile(`^\s*at (\S.*)$`)
	spaceRE     = regexp.MustCompile(`\s{2,}`)
)

type diag struct {
	file, line, col, kind, code, msg, proj string
}

type counters struct{ restore, noise, frames, dup, passed int }

func (c counters) total() int { return c.restore + c.noise + c.frames + c.dup + c.passed }

func (c counters) marker() string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(c.restore, "restore")
	add(c.noise, "noise")
	add(c.frames, "framework frames")
	add(c.dup, "duplicate")
	add(c.passed, "passed")
	return fmt.Sprintf("…+%d lines elided (%s)", c.total(), strings.Join(parts, ", "))
}

func isFormat(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if !strings.HasPrefix(a, "-") {
			return a == "format"
		}
	}
	return false
}

func (f *Formatter) aggressive(argv []string, stdout, stderr []byte) (format.Rendered, error) {
	lines := splitLines(stdout)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rel := f.relativizer()
	var (
		recognised bool
		head       []string // "X -> path" lines and unknown text, in order
		diags      []diag
		seen       = map[string]bool{}
		tests      []string
		cnt        counters
		restored   int
		upToDate   bool
		buildLine  string
		warnN      = -1
		errN       = -1
		elapsed    string
		result     string
		inTest     bool
		inStack    bool
		failDrop   int
		curFrames  int
	)
	flushFrames := func() {
		if failDrop > 0 {
			tests = append(tests, fmt.Sprintf("    …+%d framework frames", failDrop))
			failDrop = 0
		}
	}
	for _, l := range lines {
		raw := rel(l)
		t := strings.TrimSpace(raw)
		if t == "" {
			if inTest {
				flushFrames()
			}
			inTest, inStack = false, false
			continue
		}
		if m := diagRE.FindStringSubmatch(raw); m != nil && !strings.HasPrefix(raw, " ") {
			recognised = true
			if seen[raw] {
				cnt.dup++
				continue
			}
			seen[raw] = true
			d := diag{file: m[1], line: m[2], col: m[3], kind: m[4], code: m[5], msg: m[6], proj: m[7]}
			diags = append(diags, d)
			continue
		}
		switch {
		case t == "Determining projects to restore...":
			recognised = true
			cnt.restore++
			continue
		case t == "All projects are up-to-date for restore.":
			upToDate = true
			cnt.restore++
			continue
		case strings.HasPrefix(t, "Restored "):
			restored++
			cnt.restore++
			continue
		case strings.HasPrefix(t, "Failed to restore "), strings.HasPrefix(t, "Test run for "),
			strings.HasPrefix(t, "A total of "), strings.HasPrefix(t, "[xUnit.net "),
			strings.HasPrefix(t, "Microsoft (R) "), strings.HasPrefix(t, "Copyright (C)"),
			strings.HasSuffix(t, " projects are up-to-date for restore."):
			cnt.noise++
			continue
		}
		if m := testStartRE.FindStringSubmatch(raw); m != nil {
			recognised = true
			if inTest {
				flushFrames()
			}
			inTest, inStack, curFrames = false, false, 0
			switch m[1] {
			case "Failed":
				inTest = true
				tests = append(tests, "Failed "+m[2])
			case "Skipped":
				tests = append(tests, "Skipped "+m[2])
			default:
				cnt.passed++
			}
			continue
		}
		if inTest {
			switch t {
			case "Error Message:":
				inStack = false
				continue
			case "Stack Trace:":
				inStack = true
				continue
			}
			if strings.HasPrefix(t, "Standard Output") || strings.HasPrefix(t, "Standard Error") {
				inStack = false
				tests = append(tests, "  "+t)
				continue
			}
			if inStack {
				if m := frameRE.FindStringSubmatch(raw); m != nil {
					if strings.Contains(m[1], " in ") && strings.Contains(m[1], ":line ") && curFrames < 8 {
						flushFrames()
						curFrames++
						tests = append(tests, "    at "+m[1])
					} else {
						failDrop++
						cnt.frames++
					}
					continue
				}
				if strings.HasPrefix(t, "---") { // "--- End of stack trace from previous location ---"
					cnt.frames++
					continue
				}
			}
			tests = append(tests, "  "+strings.TrimPrefix(raw, "   "))
			continue
		}
		if m := summaryRE.FindStringSubmatch(t); m != nil {
			recognised = true
			result = m[1] + "! " + spaceRE.ReplaceAllString(m[2], " ")
			continue
		}
		if m := buildRE.FindStringSubmatch(t); m != nil {
			recognised = true
			buildLine = "Build " + m[1]
			continue
		}
		if m := countRE.FindStringSubmatch(raw); m != nil {
			n := atoi(m[1])
			if m[2] == "Warning" {
				warnN = n
			} else {
				errN = n
			}
			continue
		}
		if m := elapsedRE.FindStringSubmatch(t); m != nil {
			elapsed = m[1]
			continue
		}
		if m := arrowRE.FindStringSubmatch(raw); m != nil {
			recognised = true
		}
		head = append(head, strings.TrimLeft(raw, " "))
	}
	flushFrames()
	if !recognised {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	body := head
	body = append(body, renderDiags(diags, isFormat(argv))...)
	body = append(body, tests...)
	if buildLine != "" {
		s := buildLine + "."
		var counts []string
		if warnN >= 0 {
			counts = append(counts, fmt.Sprintf("%d warnings", warnN))
		}
		if errN >= 0 {
			counts = append(counts, fmt.Sprintf("%d errors", errN))
		}
		if len(counts) > 0 {
			s += " " + strings.Join(counts, ", ")
		}
		if elapsed != "" {
			s += " (" + elapsed + ")"
		}
		body = append(body, s)
	}
	if result != "" {
		body = append(body, result)
	}
	if buildLine == "" && result == "" {
		switch {
		case restored > 0:
			body = append(body, fmt.Sprintf("restored %d project(s)", restored))
		case upToDate:
			body = append(body, "restore: all projects up-to-date")
		}
	}
	if len(stderr) > 0 {
		body = append(body, strings.TrimRight(string(stderr), "\n"))
	}
	if cnt.total() > 0 {
		body = append(body, cnt.marker())
	}
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0, Elided: cnt.total() > 0}, nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// location formats file(line,col) the way MSBuild prints it.
func (d diag) location() string {
	if d.line == "" {
		return d.file
	}
	return fmt.Sprintf("%s(%s,%s)", d.file, d.line, d.col)
}

func (d diag) render(withKind bool) string {
	s := d.location()
	if withKind {
		s += fmt.Sprintf(": %s %s", d.kind, d.code)
	}
	s += ": " + d.msg
	return s
}

// renderDiags lists every error, then warnings grouped by code. Groups larger
// than the cap show their first entries and an explicit "…+N more".
func renderDiags(diags []diag, formatRun bool) []string {
	const warnCap = 3
	errCap := 25
	if formatRun {
		errCap = 5
	}
	type group struct {
		kind, code string
		items      []diag
	}
	var order []*group
	byKey := map[string]*group{}
	for _, d := range diags {
		k := d.kind + " " + d.code
		g := byKey[k]
		if g == nil {
			g = &group{kind: d.kind, code: d.code}
			byKey[k] = g
			order = append(order, g)
		}
		g.items = append(g.items, d)
	}
	var out []string
	for _, pass := range []string{"error", "warning"} {
		for _, g := range order {
			if g.kind != pass {
				continue
			}
			limit := warnCap
			if pass == "error" {
				limit = errCap
			}
			if len(g.items) <= limit && len(g.items) < 4 || (pass == "error" && len(g.items) <= limit) {
				for _, d := range g.items {
					out = append(out, d.render(true))
				}
				continue
			}
			out = append(out, fmt.Sprintf("%s %s ×%d", g.kind, g.code, len(g.items)))
			for _, d := range g.items[:min(limit, len(g.items))] {
				out = append(out, "  "+d.render(false))
			}
			if len(g.items) > limit {
				out = append(out, fmt.Sprintf("  …+%d more", len(g.items)-limit))
			}
		}
	}
	return out
}
