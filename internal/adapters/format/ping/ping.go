// Package ping formats finite `ping` runs (-c N or -t N): the per-reply lines
// collapse into one counted line per distinct (size, source, ttl) with the
// sequence range (the round-trip statistics line carries the timing), consecutive timeouts collapse into a counted range,
// and the statistics block is kept verbatim. Anything unrecognised (DUP!,
// unreachable, TTL-exceeded, resolver errors) passes through untouched.
package ping

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	// busybox prints "seq=N" where BSD and iputils print "icmp_seq=N".
	replyRE = regexp.MustCompile(`^(\d+) bytes from ([^:]+): (?:icmp_)?seq=(\d+) ttl=(\d+) time=([\d.]+) ms$`)
	// BSD says "Request timeout for icmp_seq N"; iputils -O says "no answer
	// yet for icmp_seq=N".
	timeoutRE = regexp.MustCompile(`^(?:Request timeout for icmp_seq |no answer yet for icmp_seq=)(\d+)$`)
)

// Formatter renders ping output.
type Formatter struct{}

// New constructs a ping Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every ping invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "ping"} }

// Aggressive collapses replies and timeouts.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: the reply grammar is fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

type group struct {
	bytes, from, ttl string
	n                int
	seqLo, seqHi     int
}

func render(in format.Input) (format.Rendered, error) {
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)
	var out []string
	var groups []*group
	byKey := map[string]*group{}
	slot := -1 // index in out of the replies placeholder
	timeouts, toLo, toHi, toSlot := 0, 0, 0, -1
	replies := 0
	for _, l := range lines {
		if m := replyRE.FindStringSubmatch(l); m != nil {
			seq, _ := strconv.Atoi(m[3])
			key := m[1] + "|" + m[2] + "|" + m[4]
			g := byKey[key]
			if g == nil {
				g = &group{bytes: m[1], from: m[2], ttl: m[4], seqLo: seq, seqHi: seq}
				byKey[key] = g
				groups = append(groups, g)
			}
			g.n++
			g.seqLo, g.seqHi = min(g.seqLo, seq), max(g.seqHi, seq)
			replies++
			if slot < 0 {
				slot = len(out)
				out = append(out, "")
			}
			continue
		}
		if m := timeoutRE.FindStringSubmatch(l); m != nil {
			seq, _ := strconv.Atoi(m[1])
			if timeouts == 0 {
				toLo, toHi = seq, seq
				toSlot = len(out)
				out = append(out, "")
			}
			toLo, toHi = min(toLo, seq), max(toHi, seq)
			timeouts++
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	if replies < 3 && timeouts < 3 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if slot >= 0 {
		var parts []string
		for _, g := range groups {
			parts = append(parts, fmt.Sprintf("replies ×%d: %s bytes from %s ttl=%s icmp_seq %s",
				g.n, g.bytes, g.from, g.ttl, rng(strconv.Itoa(g.seqLo), strconv.Itoa(g.seqHi))))
		}
		out[slot] = strings.Join(parts, "\n")
	}
	if toSlot >= 0 {
		out[toSlot] = fmt.Sprintf("Request timeout ×%d (icmp_seq %s)", timeouts, rng(strconv.Itoa(toLo), strconv.Itoa(toHi)))
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func rng(lo, hi string) string {
	if lo == hi {
		return lo
	}
	return lo + "-" + hi
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
