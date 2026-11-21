// Package lintkit holds the pieces shared by the linter formatters
// (shellcheck, hadolint, yamllint, markdownlint, actionlint, pre-commit):
// stream reading, ANSI stripping, machine-format flag detection, and the
// per-file grouped findings rendering with a code histogram.
package lintkit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// ReadAll drains a possibly-nil reader.
func ReadAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

// StripANSI removes terminal escape sequences.
func StripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// Lines splits raw bytes into lines without terminators (CRLF tolerated).
func Lines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

// OptionValue finds the value of a flag in argv[1:]. It understands
// `-f json`, `-fjson` (single-dash short names), `--format json` and
// `--format=json`. Scanning stops at "--".
func OptionValue(argv []string, names ...string) (string, bool) {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return "", false
		}
		for _, n := range names {
			switch {
			case a == n:
				if i+1 < len(argv) {
					return argv[i+1], true
				}
				return "", true
			case strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"="):
				return a[len(n)+1:], true
			case len(n) == 2 && n[0] == '-' && n[1] != '-' && strings.HasPrefix(a, n) && !strings.HasPrefix(a, "--"):
				return a[2:], true
			}
		}
	}
	return "", false
}

// HasFlag reports whether any of names appears as an exact argument.
func HasFlag(argv []string, names ...string) bool {
	for i := 1; i < len(argv); i++ {
		if argv[i] == "--" {
			return false
		}
		for _, n := range names {
			if argv[i] == n {
				return true
			}
		}
	}
	return false
}

// Finding is one lint result.
type Finding struct {
	File string
	Line int
	Col  int // 0 when unknown
	Sev  string
	Code string
	Msg  string
}

// Dropped counts content removed by a formatter, for the elision marker.
type Dropped struct {
	Label string
	N     int
}

// MaxMsg bounds one finding's message. Longer messages are cut with an
// explicit marker.
const MaxMsg = 240

// Clip truncates s to MaxMsg runes with a visible count of what was cut.
func Clip(s string) string {
	r := []rune(s)
	if len(r) <= MaxMsg {
		return s
	}
	return string(r[:MaxMsg]) + fmt.Sprintf("…+%d chars", len(r)-MaxMsg)
}

// Render groups findings per file (first-seen order), lists every finding with
// its line:col, and prefixes a header carrying the total and a code histogram.
// dropped entries with N>0 are announced on a trailing line.
func Render(tool string, fs []Finding, dropped ...Dropped) string {
	var order []string
	byFile := map[string][]Finding{}
	hist := map[string]int{}
	for _, f := range fs {
		if _, ok := byFile[f.File]; !ok {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
		if f.Code != "" {
			hist[f.Code]++
		}
	}
	var b strings.Builder
	seen := map[string]bool{}
	repeats := 0
	fmt.Fprintf(&b, "%s: %d %s in %d %s", tool, len(fs), plural(len(fs), "finding", "findings"),
		len(order), plural(len(order), "file", "files"))
	if h := Histogram(hist); h != "" {
		b.WriteString(" (" + h + ")")
	}
	for _, file := range order {
		list := byFile[file]
		fmt.Fprintf(&b, "\n%s (%d)", file, len(list))
		for _, f := range list {
			b.WriteString("\n  ")
			b.WriteString(Pos(f.Line, f.Col))
			if f.Sev != "" {
				b.WriteString(" " + f.Sev)
			}
			if f.Code != "" {
				b.WriteString(" " + f.Code)
			}
			key := f.Code + "\x00" + f.Msg
			if f.Code != "" && seen[key] {
				repeats++
				continue
			}
			seen[key] = true
			b.WriteString(" " + Clip(f.Msg))
		}
	}
	if repeats > 0 {
		fmt.Fprintf(&b, "\n×%d findings without text repeat the message first shown for their code", repeats)
	}
	var parts []string
	for _, d := range dropped {
		if d.N > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", d.N, d.Label))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n…dropped: " + strings.Join(parts, ", "))
	}
	return b.String()
}

// Pos formats line[:col]; a zero line renders as "-".
func Pos(line, col int) string {
	if line == 0 {
		return "-"
	}
	if col == 0 {
		return strconv.Itoa(line)
	}
	return strconv.Itoa(line) + ":" + strconv.Itoa(col)
}

// Histogram renders "CODE=n" pairs, most frequent first, ties by code.
func Histogram(h map[string]int) string {
	type kv struct {
		k string
		n int
	}
	var kvs []kv
	for k, n := range h {
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
		parts[i] = fmt.Sprintf("%s=%d", e.k, e.n)
	}
	return strings.Join(parts, " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
