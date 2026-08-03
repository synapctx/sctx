// Package psproc implements a format.Formatter for `ps`. It is a top savings
// target (large stdout on `ps aux`), so the aggressive tier is a real
// column-offset parse rather than generic line filtering.
package psproc

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `ps` output.
type Formatter struct{}

// New constructs a ps Formatter.
func New() format.Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "ps"}
}

const (
	maxCommandLen = 60
	maxRows       = 40
)

// header describes a parsed ps header row: its column names in
// left-to-right order and a name→index lookup. Data rows are split by
// whitespace token position (not byte offset) since ps right-justifies
// numeric columns and left-justifies text columns within column widths that
// aren't derivable from the header text alone.
type header struct {
	names []string
	idx   map[string]int
}

// parseHeader tokenizes a header line into whitespace-delimited column
// names.
func parseHeader(line string) (header, bool) {
	names := strings.Fields(line)
	if len(names) == 0 {
		return header{}, false
	}
	h := header{names: names, idx: make(map[string]int, len(names))}
	for i, n := range names {
		h.idx[strings.ToUpper(n)] = i
	}
	return h, true
}

// splitFields tokenizes line into exactly len(h.names) whitespace-delimited
// fields, with the last field keeping everything remaining (including
// embedded spaces), so COMMAND/CMD retains its full argv. ok is false if the
// line doesn't have enough tokens to fill every column.
func (h header) splitFields(line string) (fields []string, ok bool) {
	n := len(h.names)
	rest := line
	fields = make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		rest = strings.TrimLeft(rest, " \t")
		sp := strings.IndexAny(rest, " \t")
		if sp == -1 {
			return nil, false
		}
		fields = append(fields, rest[:sp])
		rest = rest[sp:]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, false
	}
	fields = append(fields, rest)
	return fields, true
}

// field extracts the named column's value from an already-split row.
func (h header) field(fields []string, name string) (string, bool) {
	i, ok := h.idx[name]
	if !ok || i >= len(fields) {
		return "", false
	}
	return fields[i], true
}

// psMode distinguishes the two header shapes this formatter understands.
type psMode int

const (
	modeUnknown psMode = iota
	modeAux            // ps aux: USER PID %CPU %MEM ... STAT ... COMMAND
	modePlain          // plain ps: PID TTY TIME CMD
)

// commandColumn returns the header name used for the trailing free-text
// command column, preferring COMMAND over CMD.
func commandColumn(h header) (string, bool) {
	if _, ok := h.idx["COMMAND"]; ok {
		return "COMMAND", true
	}
	if _, ok := h.idx["CMD"]; ok {
		return "CMD", true
	}
	return "", false
}

// detectMode classifies a header as aux-style, plain, or unrecognized.
func detectMode(h header) (mode psMode, cmdCol string) {
	cmdCol, hasCmd := commandColumn(h)
	if !hasCmd {
		return modeUnknown, ""
	}
	_, hasUser := h.idx["USER"]
	_, hasCPU := h.idx["%CPU"]
	_, hasMem := h.idx["%MEM"]
	_, hasStat := h.idx["STAT"]
	_, hasPID := h.idx["PID"]
	if hasUser && hasCPU && hasMem && hasStat && hasPID {
		return modeAux, cmdCol
	}
	_, hasTime := h.idx["TIME"]
	if hasPID && hasTime {
		return modePlain, cmdCol
	}
	return modeUnknown, ""
}

// process is one parsed ps row.
type process struct {
	user, pid, stat, command string
	cpu, mem                 float64
	haveCPU, haveMem         bool
}

func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// group is one collapsed output row: all processes sharing the same
// truncated command, rendered once with a ×N marker.
type group struct {
	user, pid, stat, command string
	cpu, mem                 float64
	haveCPU, haveMem         bool
	count                    int
}

// Aggressive parses ps aux (BSD-style) or plain ps headers by column offset
// and re-renders a compact, capped process list. Unrecognized headers
// degrade to the relaxed tier.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("psproc: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	h, ok := parseHeader(lines[0])
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	mode, cmdCol := detectMode(h)
	if mode == modeUnknown {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var procs []process
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, ok := h.splitFields(line)
		if !ok {
			continue
		}
		p := process{}
		if v, ok := h.field(fields, "USER"); ok {
			p.user = v
		}
		if v, ok := h.field(fields, "PID"); ok {
			p.pid = v
		}
		if v, ok := h.field(fields, "STAT"); ok {
			p.stat = v
		}
		if v, ok := h.field(fields, "%CPU"); ok {
			if f, ok := parseFloat(v); ok {
				p.cpu, p.haveCPU = f, true
			}
		}
		if v, ok := h.field(fields, "%MEM"); ok {
			if f, ok := parseFloat(v); ok {
				p.mem, p.haveMem = f, true
			}
		}
		if v, ok := h.field(fields, cmdCol); ok {
			p.command = v
		}
		procs = append(procs, p)
	}
	if len(procs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	total := len(procs)
	groups := groupByCommand(procs)

	if mode == modeAux {
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].cpu > groups[j].cpu })
	}

	var extra int
	if len(groups) > maxRows {
		extra = total - countProcesses(groups[:maxRows])
		groups = groups[:maxRows]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d processes\n", total)
	for _, g := range groups {
		writeGroup(&b, mode, g)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "…+%d more processes\n", extra)
	}

	return format.Rendered{
		Body: []byte(strings.TrimRight(b.String(), "\n")),
		Note: fmt.Sprintf("%d processes", total),
	}, nil
}

func countProcesses(groups []group) int {
	n := 0
	for _, g := range groups {
		n += g.count
	}
	return n
}

// groupByCommand collapses processes whose truncated command is identical,
// keeping the max %CPU/%MEM among the collapsed set and the first-seen
// user/pid/stat, in first-seen order.
func groupByCommand(procs []process) []group {
	var order []string
	byCmd := make(map[string]*group, len(procs))
	for _, p := range procs {
		cmd := truncate(p.command, maxCommandLen)
		g, ok := byCmd[cmd]
		if !ok {
			g = &group{user: p.user, pid: p.pid, stat: p.stat, command: cmd}
			byCmd[cmd] = g
			order = append(order, cmd)
		}
		g.count++
		if p.haveCPU && (!g.haveCPU || p.cpu > g.cpu) {
			g.cpu, g.haveCPU = p.cpu, true
		}
		if p.haveMem && (!g.haveMem || p.mem > g.mem) {
			g.mem, g.haveMem = p.mem, true
		}
	}
	groups := make([]group, 0, len(order))
	for _, cmd := range order {
		groups = append(groups, *byCmd[cmd])
	}
	return groups
}

func writeGroup(b *strings.Builder, mode psMode, g group) {
	switch mode {
	case modeAux:
		fmt.Fprintf(b, "%s %s %.1f %.1f %s %s", g.user, g.pid, g.cpu, g.mem, g.stat, g.command)
	default:
		fmt.Fprintf(b, "%s %s", g.pid, g.command)
	}
	if g.count > 1 {
		fmt.Fprintf(b, " ×%d", g.count)
	}
	b.WriteByte('\n')
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// splitLines splits raw bytes into lines without trailing newlines.
func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
