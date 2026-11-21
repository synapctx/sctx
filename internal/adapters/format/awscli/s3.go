package awscli

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	lsCapAbove = 200 // listings longer than this are capped
	lsHead     = 80
	lsTail     = 40
)

// renderLs caps a very long `s3 ls` listing to its head and tail.
func renderLs(text string) (string, bool) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= lsCapAbove {
		return "", false
	}
	n := len(lines) - lsHead - lsTail
	out := append([]string{}, lines[:lsHead]...)
	out = append(out, fmt.Sprintf("…+%d lines", n))
	out = append(out, lines[len(lines)-lsTail:]...)
	return strings.Join(out, "\n"), true
}

var (
	progressRE = regexp.MustCompile(`^Completed [0-9.]+ \S+(/[0-9.]+ \S+)?( \([^)]*\))? with [0-9]+ file\(s\) remaining`)
	opRE       = regexp.MustCompile(`^(\(dryrun\) )?(upload|download|copy|move|delete|remove): (.+)$`)
)

type opKey struct{ dry, verb, src, dst string }

// renderTransfer drops byte-progress tickers (CR-overwritten "Completed ..."
// segments) and folds runs of "verb: SRC to DST" operation lines that share a
// source and destination prefix into one line:
//
//	(dryrun) upload dry/ -> s3://b/p/ ×7: f1.txt f2.txt sub/z.log
//
// The fold is exact: each operation is SRC+rel -> DST+rel. Any line that does
// not fit the shape, or a name with whitespace, is left as written.
func renderTransfer(text string) string {
	var lines []string
	for _, seg := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		// Progress ticks are separated from the next message by \r only.
		for _, p := range strings.Split(seg, "\r") {
			if progressRE.MatchString(p) {
				continue
			}
			lines = append(lines, p)
		}
	}
	var out []string
	var curKey opKey
	var curRels []string
	var curRaw []string
	flush := func() {
		switch {
		case len(curRels) >= 3:
			head := strings.TrimSpace(curKey.dry + curKey.verb)
			out = append(out, fmt.Sprintf("%s %s -> %s ×%d: %s", head, curKey.src, curKey.dst, len(curRels), strings.Join(curRels, " ")))
		default:
			out = append(out, curRaw...)
		}
		curRels, curRaw = nil, nil
	}
	for _, l := range lines {
		k, rel, ok := parseOp(l, curKey, len(curRels) > 0)
		if !ok {
			flush()
			curKey = opKey{}
			out = append(out, l)
			continue
		}
		if len(curRels) > 0 && k != curKey {
			flush()
		}
		curKey = k
		curRels = append(curRels, rel)
		curRaw = append(curRaw, l)
	}
	flush()
	return strings.Join(out, "\n")
}

// parseOp splits an operation line. Once a run has established prefixes, a
// following line must reuse them; the first line of a run derives them from the
// common path suffix of source and destination.
func parseOp(l string, cur opKey, inRun bool) (opKey, string, bool) {
	m := opRE.FindStringSubmatch(l)
	if m == nil || (m[2] != "upload" && m[2] != "download" && m[2] != "copy" && m[2] != "move") {
		return opKey{}, "", false
	}
	if strings.Count(m[3], " to ") != 1 {
		return opKey{}, "", false
	}
	src, dst, _ := strings.Cut(m[3], " to ")
	if strings.ContainsAny(src, " \t") || strings.ContainsAny(dst, " \t") {
		return opKey{}, "", false
	}
	key := opKey{dry: m[1], verb: m[2]}
	if inRun && cur.dry == key.dry && cur.verb == key.verb &&
		strings.HasPrefix(src, cur.src) && strings.HasPrefix(dst, cur.dst) &&
		src[len(cur.src):] == dst[len(cur.dst):] && src[len(cur.src):] != "" {
		return cur, src[len(cur.src):], true
	}
	// Derive prefixes: the longest common suffix that starts on a path boundary.
	i, j := len(src), len(dst)
	for i > 0 && j > 0 && src[i-1] == dst[j-1] {
		i--
		j--
	}
	for i < len(src) && (i > 0 && src[i-1] != '/') {
		i++
		j++
	}
	if i >= len(src) {
		return opKey{}, "", false
	}
	key.src, key.dst = src[:i], dst[:j]
	return key, src[i:], true
}
