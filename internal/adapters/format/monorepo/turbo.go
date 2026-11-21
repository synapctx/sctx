package monorepo

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	turboLineRE   = regexp.MustCompile(`^(\S+?):([\w:.-]+): ?(.*)$`)
	turboCacheRE  = regexp.MustCompile(`^cache (miss|hit|bypass)(?:, [a-z ]+)? ?[0-9a-f]*$`)
	turboBulletRE = regexp.MustCompile(`^\s*• (.+)$`)
	turboStatRE   = regexp.MustCompile(`^\s*(Tasks|Cached|Time|Failed):\s+(.*?)\s*$`)
	turboErrRE    = regexp.MustCompile(`^(\S+?)#(\S+?):\s+ERROR\b`)
	turboWarnRE   = regexp.MustCompile(`^ WARNING  (.*)$`)
	turboFatalRE  = regexp.MustCompile(`^ ERROR  (.*)$`)
	turboTelemRE  = regexp.MustCompile(`^(Attention:|Turborepo now collects|This information is used to shape the Turborepo|You can learn more, including how to opt-out|https://turborepo\.dev/docs/telemetry)`)
	warnTailRE    = regexp.MustCompile(`(?i)\b(warn(ing)?|error|fail(ed|ure)?|✖|⚠)\b`)
)

// RenderTurbo is the aggressive tier for turbo output, exported for the
// package-manager / npx delegation.
func RenderTurbo(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[min(1, len(in.Argv)):] {
		switch a {
		case "--dry", "--dry=json", "--dry-run", "--graph", "--help", "-h", "--version", "--summarize":
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(a, "--graph=") || strings.HasPrefix(a, "--dry=") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		ts       = newTaskSet()
		bullets  []string
		stats    []string
		failedL  string
		warns    []string
		fatal    []string
		other    []string
		hidden   int
		sawTurbo bool
		inWarn   bool
		failedBy = map[string]bool{}
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := turboBulletRE.FindStringSubmatch(l); m != nil {
			sawTurbo = sawTurbo || strings.HasPrefix(m[1], "turbo ")
			bullets = append(bullets, strings.TrimSpace(m[1]))
			continue
		}
		if m := turboStatRE.FindStringSubmatch(l); m != nil {
			if m[1] == "Failed" {
				failedL = m[2]
			} else {
				stats = append(stats, m[1]+": "+strings.Join(strings.Fields(m[2]), " "))
			}
			continue
		}
		if m := turboWarnRE.FindStringSubmatch(l); m != nil {
			inWarn = true
			warns = append(warns, m[1])
			continue
		}
		if m := turboFatalRE.FindStringSubmatch(l); m != nil {
			fatal = append(fatal, "ERROR "+m[1])
			continue
		}
		if m := turboErrRE.FindStringSubmatch(l); m != nil {
			failedBy[m[1]+":"+m[2]] = true
			ts.get(m[1] + ":" + m[2]).failed = true
			continue
		}
		if turboTelemRE.MatchString(t) {
			continue
		}
		if m := turboLineRE.FindStringSubmatch(l); m != nil && !strings.Contains(m[1], " ") {
			inWarn = false
			tk := ts.get(m[1] + ":" + m[2])
			body := m[3]
			switch {
			case turboCacheRE.MatchString(body):
				if strings.Contains(body, "hit") {
					tk.note = "cache hit"
				} else if strings.Contains(body, "bypass") {
					tk.note = "cache bypass"
				}
			case strings.TrimSpace(body) == "":
			case scriptEcho(body):
				tk.hidden++
			default:
				tk.logs = append(tk.logs, body)
			}
			continue
		}
		if t == "" {
			continue
		}
		if inWarn {
			// Continuation of a multi-line WARNING block.
			hidden++
			continue
		}
		other = append(other, t)
	}
	if !sawTurbo && len(ts.order) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(stats) == 0 && len(fatal) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Failed tasks named in the summary: "pkg#task, pkg#task".
	for _, f := range strings.FieldsFunc(failedL, func(r rune) bool { return r == ',' || r == ' ' }) {
		if i := strings.LastIndex(f, "#"); i > 0 {
			k := f[:i] + ":" + f[i+1:]
			failedBy[k] = true
			ts.get(k).failed = true
		}
	}
	if in.ExitCode != 0 && len(failedBy) == 0 && len(fatal) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Failed task logs: drop npm's path-only echo lines, counted.
	for _, tk := range ts.order {
		if !tk.failed {
			continue
		}
		kept := tk.logs[:0:0]
		for _, l := range tk.logs {
			if pathOnlyNpmError(l) {
				tk.hidden++
				continue
			}
			kept = append(kept, l)
		}
		tk.logs = kept
	}
	// Succeeded tasks keep only lines that look like warnings or errors.
	for _, tk := range ts.order {
		if tk.failed {
			continue
		}
		var keep []string
		drop := 0
		for _, l := range tk.logs {
			if warnTailRE.MatchString(l) {
				keep = append(keep, l)
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
	b.WriteString("turbo: " + strings.Join(bullets, "; "))
	for _, w := range warns {
		b.WriteString("\nWARN " + w)
	}
	h := ts.render(&b)
	for _, o := range other {
		b.WriteString("\n" + o)
	}
	for _, s := range stats {
		b.WriteString("\n" + s)
	}
	if failedL != "" {
		b.WriteString("\nFailed: " + failedL)
	}
	for _, f := range fatal {
		b.WriteString("\n" + f)
	}
	h += hidden
	elided := h > 0
	if h > 0 {
		b.WriteString("\n…+" + strconv.Itoa(h) + " task log/banner lines (succeeded tasks, script echo)")
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(stderr) > 0, Elided: elided}, nil
}
