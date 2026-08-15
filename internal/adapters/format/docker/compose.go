package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// composeMarkerLineRe matches a compose "up" progress line prefixed with a
// checkmark/spinner glyph, e.g. "✔ Container myapp-web-1  Started  0.3s".
var composeMarkerLineRe = regexp.MustCompile(`^[✔✘⠋]`)

// composePullNoiseRe matches compose's per-service image-pull progress
// verbs, collapsed to a single count.
var composePullNoiseRe = regexp.MustCompile(`\b(Pulling|Pulled|Waiting|Downloading|Download complete|Extracting|Pull complete|Already exists)\b`)

// composeRemovalRe matches `docker compose down`'s per-resource removal
// lines, e.g. "Stopping myapp-web-1 ... done" / "Removing network ...".
var composeRemovalRe = regexp.MustCompile(`^(Stopping|Removing|Going to remove) `)

var composeLifecycleStatuses = map[string]bool{
	"Creating": true, "Created": true, "Starting": true, "Started": true,
	"Stopping": true, "Stopped": true, "Removing": true, "Removed": true,
	"Recreating": true, "Recreated": true, "Restarting": true, "Restarted": true,
	"Running": true, "Waiting": true, "Healthy": true,
}

// aggressiveComposePs parses the default `docker compose ps` table into one
// compact line per service: "service state ports".
func aggressiveComposePs(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 containers")}, nil
	}

	names, starts := parseHeader(lines[0])
	serviceIdx, statusIdx, portsIdx := colIndex(names, "SERVICE"), colIndex(names, "STATUS"), colIndex(names, "PORTS")
	if colIndex(names, "NAME") < 0 || serviceIdx < 0 || statusIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 containers")}, nil
	}

	up := 0
	var b strings.Builder
	for _, cols := range rows {
		state := compactStatus(cols[statusIdx])
		if strings.HasPrefix(state, "up") {
			up++
		}
		ports := ""
		if portsIdx >= 0 {
			ports = cols[portsIdx]
		}
		fmt.Fprintf(&b, "\n%s %s %s", cols[serviceIdx], state, ports)
	}

	body := fmt.Sprintf("%d containers (%d up)", len(rows), up) + strings.TrimRight(b.String(), " ")
	return format.Rendered{Body: []byte(body)}, nil
}

// aggressiveComposeUp compacts `docker compose up` output: per-resource
// checkmark progress lines ("✔ Container web-1  Started  0.3s") are
// compacted to "kind name: status", per-service image-pull progress is
// collapsed to a single "…+N pull lines" marker, "Attaching to ..." and
// everything after it (the interleaved, container-prefixed log stream, for
// a foreground `up`) collapses exact repetitions only.
func aggressiveComposeUp(in format.Input) (format.Rendered, error) {
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	raw := append(append([]byte(nil), rawOut...), rawErr...)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if out, ok := renderComposeLifecycle(lines, len(rawErr) > 0); ok {
		return out, nil
	}

	var out []string
	var logLines []string
	inLogs := false
	pullNoise := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if inLogs {
			logLines = append(logLines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "Attaching to") {
			out = append(out, trimmed)
			inLogs = true
			continue
		}
		if composeMarkerLineRe.MatchString(trimmed) {
			fields := strings.Fields(strings.TrimLeftFunc(trimmed, func(r rune) bool {
				return r == '✔' || r == '✘' || r == '⠋' || r == ' '
			}))
			if len(fields) >= 3 {
				kind, name, status := fields[0], strings.Trim(fields[1], `"`), strings.ToLower(fields[2])
				out = append(out, fmt.Sprintf("%s %s: %s", kind, name, status))
				continue
			}
		}
		if composePullNoiseRe.MatchString(trimmed) {
			pullNoise++
			continue
		}
		out = append(out, trimmed)
	}

	if pullNoise > 0 {
		out = append(out, fmt.Sprintf("…+%d pull lines", pullNoise))
	}
	if len(logLines) > 0 {
		out = append(out, filterRelaxedLines(logLines)...)
	}
	if len(out) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(out, "\n")), FoldStderr: len(rawErr) > 0}, nil
}

// aggressiveComposeDown compacts `docker compose down`'s per-resource
// removal lines ("Stopping web-1 ... done", "Removing network ...") to a
// capped list with an explicit elision marker.
func aggressiveComposeDown(in format.Input) (format.Rendered, error) {
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	raw := append(append([]byte(nil), rawOut...), rawErr...)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if out, ok := renderComposeLifecycle(lines, len(rawErr) > 0); ok {
		return out, nil
	}

	var removals []string
	var other []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if composeRemovalRe.MatchString(trimmed) {
			removals = append(removals, trimmed)
			continue
		}
		other = append(other, trimmed)
	}
	if len(removals) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	capped, elided := capRows(toRows(removals), maxListRows)
	var b strings.Builder
	fmt.Fprintf(&b, "%d removals", len(removals))
	for _, r := range other {
		b.WriteString("\n")
		b.WriteString(r)
	}
	for _, r := range capped {
		b.WriteString("\n")
		b.WriteString(r[0])
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(rawErr) > 0}, nil
}

// renderComposeLifecycle handles Compose v5's native non-TTY progress shape:
// `Container project-api-1 Creating`. It keeps each resource's terminal state
// and marks the exact number of superseded transition lines.
func renderComposeLifecycle(lines []string, foldStderr bool) (format.Rendered, bool) {
	type resource struct {
		kind, name, state string
	}
	var order []string
	resources := map[string]*resource{}
	transitions := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 || (fields[0] != "Container" && fields[0] != "Network" && fields[0] != "Volume") || !composeLifecycleStatuses[fields[len(fields)-1]] {
			return format.Rendered{}, false
		}
		key := fields[0] + "\x00" + strings.Join(fields[1:len(fields)-1], " ")
		r, exists := resources[key]
		if !exists {
			r = &resource{kind: fields[0], name: strings.Join(fields[1:len(fields)-1], " ")}
			resources[key] = r
			order = append(order, key)
		} else {
			transitions++
		}
		r.state = strings.ToLower(fields[len(fields)-1])
	}
	if len(resources) == 0 {
		return format.Rendered{}, false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d resources", len(resources))
	if transitions > 0 {
		fmt.Fprintf(&b, " (…+%d transitions)", transitions)
	}
	for _, key := range order {
		r := resources[key]
		fmt.Fprintf(&b, "\n%s %s: %s", r.kind, r.name, r.state)
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: foldStderr}, true
}

// toRows wraps single-column string lines as 1-element rows so they can
// reuse capRows.
func toRows(lines []string) [][]string {
	rows := make([][]string, len(lines))
	for i, l := range lines {
		rows[i] = []string{l}
	}
	return rows
}
