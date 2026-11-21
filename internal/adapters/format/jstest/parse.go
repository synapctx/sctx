package jstest

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	frameRE     = regexp.MustCompile(`^(?:at\s+(?:.*?\()?|❯\s+(?:\S+\s+)?)([^\s()]+):(\d+):(\d+)\)?$`)
	codeFrameRE = regexp.MustCompile(`^(?:>\s*)?\d*\s*\|`)
	jestSuiteRE = regexp.MustCompile(`^Test Suites:\s+(.*)$`)
	jestTestsRE = regexp.MustCompile(`^Tests:\s+(.*)$`)
	jestTimeRE  = regexp.MustCompile(`^Time:\s+(.*)$`)
	jestBlockRE = regexp.MustCompile(`^  ● (.+)$`)
	jestFileRE  = regexp.MustCompile(`^(?:PASS|FAIL)\s+(\S.*)$`)
	vtFilesRE   = regexp.MustCompile(`^\s*Test Files\s+(.*)$`)
	vtTestsRE   = regexp.MustCompile(`^\s*Tests\s+(.*)$`)
	vtDurRE     = regexp.MustCompile(`^\s*Duration\s+(\S+)`)
	vtFailRE    = regexp.MustCompile(`^\s*FAIL\s+(\S.*)$`)
	vtSepRE     = regexp.MustCompile(`^⎯{2,}`)
	covSepRE    = regexp.MustCompile(`^-+\|-+\|`)
	pairRE      = regexp.MustCompile(`(\d+) (\w+)`)
)

// processBody turns a failure block's raw lines into message lines plus the
// first user-code stack frame, counting everything it drops.
func processBody(raw []string) (msg []string, frame string, hidden, codeFrames int) {
	for _, l := range raw {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "at ") || strings.HasPrefix(t, "❯") {
			if m := frameRE.FindStringSubmatch(t); m != nil && frame == "" && jsutil.IsUserPath(m[1]) {
				frame = m[1] + ":" + m[2] + ":" + m[3]
			} else {
				hidden++
			}
			continue
		}
		if codeFrameRE.MatchString(t) {
			codeFrames++
			continue
		}
		n := 0
		for n < 4 && n < len(l) && l[n] == ' ' {
			n++
		}
		msg = append(msg, l[n:])
	}
	return msg, frame, hidden, codeFrames
}

func newFailure(file, title string, raw []string) failure {
	msg, frame, hidden, cf := processBody(raw)
	return failure{file: file, title: title, msg: msg, frame: frame, hiddenStack: hidden, codeFrames: cf}
}

// parseCounts splits "1 failed, 3 passed, 4 total" (or vitest's "|" form) into
// ordered "N word" pairs.
func parseCounts(s string) (counts []string, failed, total int) {
	if i := strings.Index(s, "("); i >= 0 {
		if m := regexp.MustCompile(`\((\d+)\)`).FindStringSubmatch(s[i:]); m != nil {
			total, _ = strconv.Atoi(m[1])
		}
		s = s[:i]
	}
	for _, m := range pairRE.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "total":
			total = n
		case "failed":
			failed = n
			counts = append(counts, m[0])
		default:
			counts = append(counts, m[0])
		}
	}
	return counts, failed, total
}

func parseJest(lines []string) (result, bool) {
	r := result{kind: "jest"}
	var (
		file     string
		title    string
		body     []string
		inBlock  bool
		seen     = map[string]bool{}
		hasSuite bool
		inCov    bool
		cov      []string
	)
	flush := func() {
		if !inBlock {
			return
		}
		inBlock = false
		key := file + "|" + title
		if title == "Console" || seen[key] {
			return
		}
		seen[key] = true
		r.failures = append(r.failures, newFailure(file, title, body))
	}
	for _, l := range lines {
		if m := jestBlockRE.FindStringSubmatch(l); m != nil {
			flush()
			inBlock, title, body = true, m[1], nil
			continue
		}
		if m := jestSuiteRE.FindStringSubmatch(l); m != nil {
			flush()
			_, failed, total := parseCounts(m[1])
			r.suitesTotal, r.suitesFailed, hasSuite = total, failed, true
			continue
		}
		if m := jestTestsRE.FindStringSubmatch(l); m != nil {
			flush()
			r.counts, r.testsFailed, _ = parseCounts(m[1])
			r.hasSummary = true
			continue
		}
		if m := jestTimeRE.FindStringSubmatch(l); m != nil {
			r.duration = jestDuration(m[1])
			continue
		}
		if covSepRE.MatchString(l) {
			flush()
			inCov = true
			cov = append(cov, l)
			continue
		}
		if inCov {
			if strings.Contains(l, "|") {
				cov = append(cov, l)
				continue
			}
			inCov = false
		}
		if m := jestFileRE.FindStringSubmatch(l); m != nil {
			flush()
			file = m[1]
			continue
		}
		if strings.HasPrefix(l, "Summary of all failing tests") || strings.HasPrefix(l, "Ran all test suites") ||
			strings.HasPrefix(l, "Snapshots:") || strings.HasPrefix(l, "RUNS ") {
			flush()
			continue
		}
		if inBlock {
			body = append(body, l)
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "✓") || strings.HasPrefix(t, "✕") || strings.HasPrefix(t, "○") {
			continue
		}
		r.dropped++
	}
	flush()
	if !r.hasSummary || !hasSuite {
		return result{}, false
	}
	r.coverage = trimCoverage(cov)
	return r, true
}

// jestDuration keeps the measured time and drops jest's ", estimated N s".
func jestDuration(s string) string {
	if i := strings.Index(s, ","); i >= 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), "")
}

// trimCoverage keeps the coverage table's header, the "All files" row and every
// file below 100% on any metric; fully covered files fold into one counted
// marker. The table is decision-relevant, so it is never dropped wholesale.
func trimCoverage(cov []string) []string {
	if len(cov) == 0 {
		return nil
	}
	var out []string
	full := 0
	for i, l := range cov {
		cells := strings.Split(l, "|")
		if i < 3 || covSepRE.MatchString(l) || strings.Contains(l, "All files") || len(cells) < 5 {
			out = append(out, l)
			continue
		}
		all100 := true
		for _, c := range cells[1:5] {
			if strings.TrimSpace(c) != "100" {
				all100 = false
			}
		}
		if all100 {
			full++
			continue
		}
		out = append(out, l)
	}
	if full > 0 {
		out = append(out, "…+"+strconv.Itoa(full)+" files at 100% coverage")
	}
	return out
}

func parseVitest(lines []string) (result, bool) {
	r := result{kind: "vitest"}
	var (
		file, title string
		body        []string
		inBlock     bool
		done        bool // past the summary; only trailer lines remain
		hasFiles    bool
	)
	flush := func() {
		if inBlock {
			r.failures = append(r.failures, newFailure(file, title, body))
		}
		inBlock = false
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if done {
			if strings.HasPrefix(t, "Errors") || strings.Contains(t, "Unhandled") {
				return result{}, false
			}
			continue
		}
		if vtSepRE.MatchString(t) {
			flush()
			if strings.Contains(t, "Unhandled") {
				return result{}, false
			}
			continue
		}
		if m := vtFailRE.FindStringSubmatch(l); m != nil && !strings.HasPrefix(t, "FAIL:") {
			flush()
			head := m[1]
			if i := strings.Index(head, " > "); i >= 0 {
				file, title = head[:i], strings.ReplaceAll(head[i+3:], " > ", " › ")
			} else {
				file, title = head, "suite failed to run"
			}
			// "FAIL  [project] file > ..." keeps the project tag with the file.
			inBlock, body = true, nil
			continue
		}
		if m := vtFilesRE.FindStringSubmatch(l); m != nil {
			flush()
			_, failed, total := parseCounts(strings.ReplaceAll(m[1], "|", ","))
			r.suitesTotal, r.suitesFailed, hasFiles = total, failed, true
			continue
		}
		if m := vtTestsRE.FindStringSubmatch(l); m != nil {
			flush()
			r.counts, r.testsFailed, _ = parseCounts(strings.ReplaceAll(m[1], "|", ","))
			r.hasSummary = true
			continue
		}
		if m := vtDurRE.FindStringSubmatch(l); m != nil {
			r.duration = m[1]
			done = true
			continue
		}
		if inBlock {
			body = append(body, l)
			continue
		}
		if t == "" || strings.HasPrefix(t, "RUN ") || strings.HasPrefix(t, "Start at") {
			continue
		}
		if c := []rune(t)[0]; strings.ContainsRune("✓✗×❯↓✕", c) {
			continue
		}
		r.dropped++
	}
	flush()
	if !r.hasSummary || !hasFiles {
		return result{}, false
	}
	return r, true
}

// jsonReport is the shape shared by `jest --json` and vitest's json reporter.
type jsonReport struct {
	NumTotalTestSuites  *int `json:"numTotalTestSuites"`
	NumFailedTestSuites int  `json:"numFailedTestSuites"`
	NumTotalTests       *int `json:"numTotalTests"`
	NumPassedTests      int  `json:"numPassedTests"`
	NumFailedTests      int  `json:"numFailedTests"`
	NumPendingTests     int  `json:"numPendingTests"`
	TestResults         []struct {
		Name             string `json:"name"`
		Status           string `json:"status"`
		Message          string `json:"message"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

// parseJSON handles a reporter's JSON document arriving alone on stdout.
func parseJSON(stdout, stderr []byte, kind string, in format.Input) (result, bool) {
	t := bytes.TrimSpace(stdout)
	if len(t) == 0 || t[0] != '{' || len(bytes.TrimSpace(stderr)) > 0 {
		return result{}, false
	}
	var j jsonReport
	if json.Unmarshal(t, &j) != nil || j.NumTotalTests == nil || j.NumTotalTestSuites == nil {
		return result{}, false
	}
	if kind == "" {
		kind = "jest"
	}
	r := result{kind: kind, hasSummary: true, suitesTotal: *j.NumTotalTestSuites,
		suitesFailed: j.NumFailedTestSuites, testsFailed: j.NumFailedTests}
	if j.NumFailedTests > 0 {
		r.counts = append(r.counts, strconv.Itoa(j.NumFailedTests)+" failed")
	}
	if j.NumPendingTests > 0 {
		r.counts = append(r.counts, strconv.Itoa(j.NumPendingTests)+" skipped")
	}
	r.counts = append(r.counts, strconv.Itoa(j.NumPassedTests)+" passed")
	if in.Duration > 0 {
		r.duration = in.Duration.Round(10e6).String()
	}
	for _, tr := range j.TestResults {
		n := 0
		for _, a := range tr.AssertionResults {
			if a.Status != "failed" {
				continue
			}
			n++
			var raw []string
			for _, m := range a.FailureMessages {
				raw = append(raw, jsutil.Lines([]byte(m))...)
			}
			r.failures = append(r.failures, newFailure(tr.Name, strings.ReplaceAll(a.FullName, " > ", " › "), raw))
		}
		if n == 0 && tr.Status == "failed" {
			r.failures = append(r.failures, newFailure(tr.Name, "suite failed to run", jsutil.Lines([]byte(tr.Message))))
		}
	}
	return r, true
}
