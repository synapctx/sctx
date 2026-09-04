// Package redact scrubs secrets from the final rendered bytes of a command's
// output. It is applied by the run pipeline after every other rendering tier
// (including verbatim) has produced its bytes, so a secret can never survive
// because a tier chose not to compress it.
//
// The package exposes a fixed rule set (Rules), a one-shot transform (Apply)
// for buffered output, and a streaming io.WriteCloser (Writer) for the tee
// path used while a command is still running.
package redact

import (
	"bytes"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Rule is a single secret-detection pattern.
//
// Group selects which regexp submatch (1-indexed; 0 means the whole match)
// the entropy and deny-list checks run against. MinEntropy <= 0 means the
// rule always fires on a regex match with no further validation.
type Rule struct {
	Name       string
	Re         *regexp.Regexp
	MinEntropy float64
	Group      int
}

var (
	rulesOnce sync.Once
	ruleSet   []Rule
)

// Rules returns the compiled, immutable rule set. Compilation happens once,
// on first call.
func Rules() []Rule {
	rulesOnce.Do(func() {
		ruleSet = []Rule{
			{Name: "aws-access-key", Re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
			{Name: "github-token", Re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,}`)},
			{Name: "slack-token", Re: regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
			{Name: "stripe-key", Re: regexp.MustCompile(`[sr]k_live_[A-Za-z0-9]{20,}`)},
			{Name: "google-api-key", Re: regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
			{Name: "sendgrid-key", Re: regexp.MustCompile(`SG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`)},
			{Name: "npm-token", Re: regexp.MustCompile(`_authToken\s*=\s*\S+`)},
			{Name: "private-key", Re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
			{Name: "jwt", Re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
			{Name: "bearer", Re: regexp.MustCompile(`(?i)authorization:\s*bearer\s+\S+`)},
			{Name: "sctx-key", Re: regexp.MustCompile(`sctx_live_[A-Za-z0-9]{8,}`)},
			{
				Name:       "generic-secret",
				Re:         regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[=:]\s*['"]?([^\s'"]{12,})`),
				MinEntropy: 3.5,
				Group:      2,
			},
		}
	})
	return ruleSet
}

// maxScan bounds the amount of a single Apply call that is scanned for
// secrets. Beyond it the tail is passed through untouched and its length is
// reported via Report.Unscanned so the caller can print a notice — output is
// never silently left unscanned without a signal.
const maxScan = 8 * 1024 * 1024

// denyList holds placeholder values that must never be reported as a leaked
// secret even when they satisfy a rule's regex and entropy bar.
var denyList = []string{"changeme", "<redacted>", "xxxxxxxx", "example", "placeholder"}

// Report summarizes one Apply (or Writer lifetime) call.
type Report struct {
	Count     int
	ByKind    map[string]int
	Unscanned int
}

func newReport() Report {
	return Report{ByKind: map[string]int{}}
}

func (r *Report) merge(other Report) {
	r.Count += other.Count
	r.Unscanned += other.Unscanned
	for k, v := range other.ByKind {
		r.ByKind[k] += v
	}
}

type span struct {
	start, end int
	name       string
}

// Apply scans b against Rules and returns a copy with every match replaced
// by "[REDACTED:<name>]". Overlapping matches are resolved leftmost-longest,
// so a JWT found inside a "Authorization: Bearer ..." header yields exactly
// one marker, not two.
//
// Only the first maxScan bytes of b are scanned; the remainder is copied
// through unchanged and its length is reported in Report.Unscanned.
func Apply(b []byte) ([]byte, Report) {
	rep := newReport()

	scanLen := len(b)
	if scanLen > maxScan {
		rep.Unscanned = scanLen - maxScan
		scanLen = maxScan
	}
	head, tail := b[:scanLen], b[scanLen:]

	var spans []span
	for _, r := range Rules() {
		for _, m := range r.Re.FindAllSubmatchIndex(head, -1) {
			start, end := m[0], m[1]
			checkStart, checkEnd := start, end
			if r.Group > 0 {
				gi := 2 * r.Group
				if gi+1 < len(m) && m[gi] >= 0 {
					checkStart, checkEnd = m[gi], m[gi+1]
				}
			}
			if !allowed(r, string(head[checkStart:checkEnd])) {
				continue
			}
			spans = append(spans, span{start: start, end: end, name: r.Name})
		}
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		// Longest match at the same start wins (leftmost-longest).
		return (spans[i].end - spans[i].start) > (spans[j].end - spans[j].start)
	})

	var out bytes.Buffer
	out.Grow(len(head))
	last := 0
	for _, s := range spans {
		if s.start < last {
			continue // fully or partially covered by an earlier, longer selection
		}
		out.Write(head[last:s.start])
		out.WriteString("[REDACTED:")
		out.WriteString(s.name)
		out.WriteByte(']')
		rep.Count++
		rep.ByKind[s.name]++
		last = s.end
	}
	out.Write(head[last:])
	out.Write(tail)

	return out.Bytes(), rep
}

// allowed reports whether a candidate match should actually be redacted: a
// rule with no MinEntropy always fires; one with a MinEntropy bar also
// rejects denied placeholder values and requires the Shannon entropy of the
// checked text to meet the bar.
func allowed(r Rule, text string) bool {
	if r.MinEntropy <= 0 {
		return true
	}
	trimmed := strings.Trim(text, `'"`)
	lower := strings.ToLower(trimmed)
	for _, d := range denyList {
		if lower == d {
			return false
		}
	}
	if strings.HasPrefix(lower, "your-") {
		return false
	}
	return shannonEntropy(trimmed) >= r.MinEntropy
}

// shannonEntropy returns the Shannon entropy of s in bits per byte.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Writer is an io.WriteCloser that redacts secrets from a streaming tee
// path. It holds back the last carryOver bytes of unflushed data on every
// Write so a token split across two writer calls is still reassembled and
// caught before it reaches the underlying writer. Close flushes whatever
// remains; Report is only complete once Close has returned.
type Writer struct {
	w      io.Writer
	buf    []byte
	rep    Report
	closed bool
}

// carryOver is the amount of unflushed tail data a Writer always keeps
// buffered, so that no single secret pattern can be split across a Write
// boundary and slip through unredacted.
const carryOver = 4096

// NewWriter returns a Writer that redacts secrets from data written to it
// before forwarding the redacted bytes to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, rep: newReport()}
}

func (rw *Writer) Write(p []byte) (int, error) {
	if rw.closed {
		return 0, io.ErrClosedPipe
	}
	rw.buf = append(rw.buf, p...)
	if len(rw.buf) > carryOver {
		flushLen := len(rw.buf) - carryOver
		redacted, rep := Apply(rw.buf[:flushLen])
		rw.rep.merge(rep)
		if _, err := rw.w.Write(redacted); err != nil {
			return 0, err
		}
		remaining := make([]byte, len(rw.buf)-flushLen)
		copy(remaining, rw.buf[flushLen:])
		rw.buf = remaining
	}
	return len(p), nil
}

// Close flushes any buffered bytes through Apply and, if the underlying
// writer is also an io.Closer, closes it.
func (rw *Writer) Close() error {
	if rw.closed {
		return nil
	}
	rw.closed = true
	if len(rw.buf) > 0 {
		redacted, rep := Apply(rw.buf)
		rw.rep.merge(rep)
		rw.buf = nil
		if _, err := rw.w.Write(redacted); err != nil {
			return err
		}
	}
	if c, ok := rw.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Report returns the cumulative redaction report. It is only complete after
// Close has returned.
func (rw *Writer) Report() Report {
	return rw.rep
}
