package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// hashLineRe matches BuildKit's plain-progress step prefix, e.g.
// "#7 [3/5] RUN npm install" or "#7 DONE 0.3s".
var hashLineRe = regexp.MustCompile(`^#(\d+) (.*)$`)

// legacyStepRe matches the classic (non-BuildKit) `docker build` step
// header, e.g. "Step 3/5 : RUN npm install".
var legacyStepRe = regexp.MustCompile(`^Step (\d+)/(\d+) : (.*)$`)

// durationSuffixRe recognizes a trailing "Ns"/"N.Ms" duration column, used to
// strip BuildKit's fancy "=>" progress alignment padding.
var durationSuffixRe = regexp.MustCompile(`^[\d.]+s$`)

// aggressiveBuild compacts `docker build` progress output (BuildKit's fancy
// "=>" progress, BuildKit's plain "#N" progress, and the legacy
// "Step N/M :" format) down to one line per build step, dropping per-layer
// transfer/progress noise while always keeping the final image reference
// lines (naming/writing image/Successfully built|tagged) verbatim.
//
// This only ever sees ExitCode == 0 (Aggressive degrades any non-zero exit
// to Relaxed before reaching here), so failure output does not need special
// handling: Relaxed's line-level filtering already preserves ERROR/failure
// lines verbatim.
func aggressiveBuild(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if steps, final, noise, ok := parseHashProgress(lines); ok {
		return renderBuildSteps(steps, final, noise)
	}
	if steps, final, noise, ok := parseArrowProgress(lines); ok {
		return renderBuildSteps(steps, final, noise)
	}
	if steps, final, noise, ok := parseLegacyProgress(lines); ok {
		return renderBuildSteps(steps, final, noise)
	}
	return format.Rendered{}, format.ErrTierInapplicable
}

// hashStepInfo accumulates a single BuildKit plain-progress step's title
// (the step's description, e.g. "[3/5] RUN npm install") and its terminal
// status ("done 0.3s", "cached", ...).
type hashStepInfo struct {
	title  string
	status string
}

// parseHashProgress parses BuildKit's "#N ..." plain-progress format
// (docker build --progress=plain), grouping lines by step number.
func parseHashProgress(lines []string) (steps []string, final []string, noise int, ok bool) {
	var order []string
	info := map[string]*hashStepInfo{}
	for _, line := range lines {
		m := hashLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ok = true
		id, rest := m[1], strings.TrimSpace(m[2])
		si, exists := info[id]
		if !exists {
			si = &hashStepInfo{}
			info[id] = si
			order = append(order, id)
		}
		switch {
		case strings.HasPrefix(rest, "DONE"):
			si.status = "done " + strings.TrimSpace(strings.TrimPrefix(rest, "DONE"))
		case rest == "CACHED":
			si.status = "cached"
		case strings.HasPrefix(rest, "ERROR"):
			si.status = rest
		case strings.HasPrefix(rest, "naming to ") || strings.HasPrefix(rest, "writing image sha256:"):
			final = append(final, "#"+id+" "+rest)
		case strings.HasPrefix(rest, "transferring") || strings.HasPrefix(rest, "sha256:") ||
			strings.HasPrefix(rest, "extracting") || strings.HasPrefix(rest, "exporting layers") ||
			strings.HasPrefix(rest, "writing") || strings.HasPrefix(rest, "resolve "):
			noise++
		default:
			if si.title == "" {
				si.title = rest
			} else {
				noise++
			}
		}
	}
	if !ok {
		return nil, nil, 0, false
	}
	for _, id := range order {
		si := info[id]
		title := si.title
		if title == "" {
			title = "(step " + id + ")"
		}
		status := si.status
		if status == "" {
			status = "running"
		}
		steps = append(steps, fmt.Sprintf("#%s %s %s", id, title, status))
	}
	return steps, final, noise, true
}

// parseArrowProgress parses BuildKit's default fancy "=>" progress format,
// keeping one line per top-level "=>" step and dropping nested "=> =>"
// detail lines except the terminal naming/writing-image lines.
func parseArrowProgress(lines []string) (steps []string, final []string, noise int, ok bool) {
	seenFinal := map[string]bool{}
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "[+] Building") {
			ok = true
			continue
		}
		if !strings.HasPrefix(trimmed, "=>") {
			continue
		}
		ok = true
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "=>"))
		double := false
		if strings.HasPrefix(rest, "=>") {
			double = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "=>"))
		}
		rest = stripTrailingDuration(rest)
		if double {
			if strings.HasPrefix(rest, "naming to ") || strings.HasPrefix(rest, "writing image sha256:") {
				if !seenFinal[rest] {
					final = append(final, rest)
					seenFinal[rest] = true
				}
				continue
			}
			noise++
			continue
		}
		steps = append(steps, rest)
	}
	if !ok {
		return nil, nil, 0, false
	}
	return steps, final, noise, true
}

// stripTrailingDuration removes BuildKit's right-aligned "  0.3s" duration
// column from an "=>" progress line, if present.
func stripTrailingDuration(s string) string {
	idx := strings.LastIndex(s, "  ")
	if idx < 0 {
		return s
	}
	tail := strings.TrimSpace(s[idx:])
	if durationSuffixRe.MatchString(tail) {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// parseLegacyProgress parses the classic (pre-BuildKit) `docker build`
// format: one "Step N/M : ..." line per instruction, "--->"/"Removing
// intermediate container" noise in between, and a final "Successfully
// built"/"Successfully tagged" pair.
func parseLegacyProgress(lines []string) (steps []string, final []string, noise int, ok bool) {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case legacyStepRe.MatchString(trimmed):
			ok = true
			m := legacyStepRe.FindStringSubmatch(trimmed)
			steps = append(steps, fmt.Sprintf("Step %s/%s : %s", m[1], m[2], m[3]))
		case strings.HasPrefix(trimmed, "Successfully built ") || strings.HasPrefix(trimmed, "Successfully tagged "):
			ok = true
			final = append(final, trimmed)
		case strings.HasPrefix(trimmed, "--->") || strings.HasPrefix(trimmed, "Removing intermediate container"):
			noise++
		case trimmed == "":
			// ignore
		default:
			noise++
		}
	}
	if !ok {
		return nil, nil, 0, false
	}
	return steps, final, noise, true
}

// renderBuildSteps assembles the compacted body: a summary header, one line
// per step, then the terminal image-reference lines kept verbatim.
func renderBuildSteps(steps []string, final []string, noise int) (format.Rendered, error) {
	if len(steps) == 0 && len(final) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	if noise > 0 {
		fmt.Fprintf(&b, "%d build steps (…+%d lines elided)", len(steps), noise)
	} else {
		fmt.Fprintf(&b, "%d build steps", len(steps))
	}
	for _, s := range steps {
		b.WriteString("\n")
		b.WriteString(s)
	}
	for _, f := range final {
		b.WriteString("\n")
		b.WriteString(f)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}
