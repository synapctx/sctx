package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var exitStatusRe = regexp.MustCompile(`^Exited \((\d+)\)`)

// compactStatus compresses a docker STATUS field into a short state token,
// e.g. "Up 3 hours (healthy)" -> "up·healthy", "Exited (1) 2 hours ago" ->
// "exit(1)".
func compactStatus(status string) string {
	if m := exitStatusRe.FindStringSubmatch(status); m != nil {
		return "exit(" + m[1] + ")"
	}
	switch {
	case strings.HasPrefix(status, "Up"):
		switch {
		case strings.Contains(status, "(healthy)"):
			return "up·healthy"
		case strings.Contains(status, "(unhealthy)"):
			return "up·unhealthy"
		case strings.Contains(status, "(starting)"):
			return "up·starting"
		default:
			return "up"
		}
	case strings.HasPrefix(status, "Created"):
		return "created"
	case strings.HasPrefix(status, "Restarting"):
		return "restarting"
	case strings.HasPrefix(status, "Paused"):
		return "paused"
	case strings.HasPrefix(status, "Dead"):
		return "dead"
	default:
		return strings.ToLower(status)
	}
}

// aggressivePs parses the default `docker ps` table into one compact line
// per container: "name state image ports".
func aggressivePs(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 containers")}, nil
	}

	names, starts := parseHeader(lines[0])
	nameIdx, imageIdx, statusIdx, portsIdx := colIndex(names, "NAMES"), colIndex(names, "IMAGE"), colIndex(names, "STATUS"), colIndex(names, "PORTS")
	if colIndex(names, "CONTAINER ID") < 0 || nameIdx < 0 || imageIdx < 0 || statusIdx < 0 {
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
		fmt.Fprintf(&b, "\n%s %s %s %s", cols[nameIdx], state, cols[imageIdx], ports)
	}

	body := fmt.Sprintf("%d containers (%d up)", len(rows), up) + strings.TrimRight(b.String(), " ")
	return format.Rendered{Body: []byte(body)}, nil
}
