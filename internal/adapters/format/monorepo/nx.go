package monorepo

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	nxBannerRE  = regexp.MustCompile(`^NX\s+(.*)$`)
	nxOKRE      = regexp.MustCompile(`^✔\s+nx run (\S+?)(?:\s+\[(.+)\])?$`)
	nxFailRE    = regexp.MustCompile(`^✖\s+nx run (\S+?)(?:\s+\[(.+)\])?$`)
	nxBlockRE   = regexp.MustCompile(`^> nx run (\S+?)(?:\s+\[(.+)\])?$`)
	nxProjRE    = regexp.MustCompile(`^- (\S+)$`)
	nxStatRE    = regexp.MustCompile(`^\s+(Run duration|Cache):\s+(.*?)\s*$`)
	nxNoiseRE   = regexp.MustCompile(`^(Re-run with --output-style=static to inline logs\.|Output of \d+ successful tasks? w(?:as|ere) not shown\..*|\s+(Critical path|Recoverable time):.*)$`)
	nxFullLogRE = regexp.MustCompile(`^\s*full log: (.+)$`)
)

// RenderNx is the aggressive tier for nx output, exported for the
// package-manager / npx delegation. It reads the default non-TTY reporter,
// `--output-style=static`, and the terse reporter nx switches to under an AI
// agent.
func RenderNx(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[min(1, len(in.Argv)):] {
		switch a {
		case "--help", "-h", "--version", "--json", "--graph", "--watch", "--dry-run":
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(a, "--output-style=") && a != "--output-style=static" && a != "--output-style=stream" && a != "--output-style=dynamic" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		ts       = newTaskSet()
		banners  []string
		projects []string
		stats    []string
		other    []string
		failedL  []string
		cur      *task
		inFailed bool
		inProj   bool
		sawNx    bool
		fullLogs []string
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := nxBannerRE.FindStringSubmatch(t); m != nil && strings.HasPrefix(l, " NX") {
			sawNx = true
			cur, inFailed = nil, false
			inProj = strings.HasSuffix(m[1], ":")
			banners = append(banners, strings.TrimSuffix(m[1], ":"))
			continue
		}
		if m := nxOKRE.FindStringSubmatch(t); m != nil {
			cur = nil
			tk := ts.get(m[1])
			tk.note = strings.TrimPrefix(m[2], "")
			continue
		}
		if m := nxFailRE.FindStringSubmatch(t); m != nil {
			cur = nil
			tk := ts.get(m[1])
			tk.failed, tk.note = true, m[2]
			continue
		}
		if m := nxBlockRE.FindStringSubmatch(t); m != nil {
			cur, inFailed, inProj = ts.get(m[1]), false, false
			cur.note = m[2]
			continue
		}
		if m := nxStatRE.FindStringSubmatch(l); m != nil {
			cur = nil
			stats = append(stats, m[1]+": "+m[2])
			continue
		}
		if nxNoiseRE.MatchString(l) {
			// Boilerplate pointers, not content: dropped without a marker.
			cur = nil
			continue
		}
		if m := nxFullLogRE.FindStringSubmatch(l); m != nil {
			fullLogs = append(fullLogs, m[1])
			continue
		}
		if t == "Failed tasks:" {
			inFailed, cur = true, nil
			continue
		}
		if m := nxProjRE.FindStringSubmatch(t); m != nil {
			if inFailed {
				failedL = append(failedL, m[1])
				continue
			}
			if inProj {
				projects = append(projects, m[1])
				continue
			}
		}
		if t == "" {
			continue
		}
		if cur != nil {
			switch {
			case scriptEcho(t):
				cur.hidden++
			case pathOnlyNpmError(t):
				cur.hidden++
			default:
				cur.logs = append(cur.logs, l)
			}
			continue
		}
		other = append(other, t)
	}
	if !sawNx {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, n := range failedL {
		ts.get(n).failed = true
	}
	// Terse reporter summary: "3 tasks: 3 succeeded" is the only count.
	nFailed := 0
	for _, tk := range ts.order {
		if tk.failed {
			nFailed++
		}
	}
	if in.ExitCode != 0 && nFailed == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(ts.order) == 0 && len(banners) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Succeeded tasks: keep only warning/error-looking log lines.
	var warns []string
	for _, tk := range ts.order {
		if tk.failed {
			continue
		}
		var keep []string
		drop := 0
		for _, l := range tk.logs {
			if warnTailRE.MatchString(l) {
				keep = append(keep, strings.TrimSpace(l))
			} else {
				drop++
			}
		}
		if len(keep) > 0 {
			warns = append(warns, tk.name+": "+strings.Join(keep, " | "))
		}
		tk.logs = make([]string, drop)
	}
	var b strings.Builder
	b.WriteString("nx: ")
	for i, bn := range banners {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(bn)
	}
	if len(projects) > 0 && len(projects) <= maxOK {
		b.WriteString(" [" + strings.Join(projects, " ") + "]")
	}
	for _, w := range warns {
		b.WriteString("\nWARN " + w)
	}
	h := ts.render(&b)
	for _, o := range other {
		b.WriteString("\n" + o)
	}
	if len(failedL) > 0 {
		b.WriteString("\nFailed: " + strings.Join(failedL, ", "))
	}
	for _, f := range fullLogs {
		b.WriteString("\nfull log: " + f)
	}
	b.WriteString("\n" + strings.Join(stats, " | "))
	elided := h > 0
	if h > 0 {
		b.WriteString("\n…+" + strconv.Itoa(h) + " task log/banner lines (succeeded tasks, script echo)")
	}
	return format.Rendered{Body: []byte(strings.TrimRight(b.String(), "\n| ")), FoldStderr: len(stderr) > 0, Elided: elided}, nil
}
