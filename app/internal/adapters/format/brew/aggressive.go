package brew

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// progressBarRe matches brew's curl-style download progress lines, e.g.
// "######################################################################## 100.0%".
var progressBarRe = regexp.MustCompile(`^#+\s*[\d.]+%$`)

// alreadyInstalledRe matches brew's no-op "already installed" advisory,
// e.g. "Warning: wget 1.21.4 is already installed and up-to-date.".
var alreadyInstalledRe = regexp.MustCompile(`already installed`)

// maxCaveatLines caps a retained ==> Caveats block; Caveats are functional
// (post-install instructions) so they are kept, not dropped, but very long
// blocks still get an elision marker for the remainder.
const maxCaveatLines = 12

// isErrorSignal reports whether line carries error/warning signal that must
// never be collapsed or dropped, even on an otherwise-noisy run.
func isErrorSignal(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "Error:") ||
		strings.HasPrefix(trimmed, "Warning:") ||
		strings.Contains(line, "curl: (")
}

// isNoiseLine reports whether line is fetch/download/pour/cleanup progress
// noise that carries no information once the run has succeeded.
func isNoiseLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case progressBarRe.MatchString(trimmed):
		return true
	case strings.HasPrefix(line, "==> Fetching"):
		return true
	case strings.HasPrefix(line, "==> Downloading"):
		return true
	case strings.HasPrefix(line, "==> Pouring"):
		return true
	case strings.HasPrefix(line, "==> Running post-install"):
		return true
	case strings.HasPrefix(line, "Already downloaded:"):
		return true
	case strings.HasPrefix(line, "==> Running `brew cleanup"):
		return true
	case strings.Contains(line, "HOMEBREW_NO_INSTALL_CLEANUP"):
		return true
	case strings.Contains(line, "HOMEBREW_NO_ENV_HINTS"):
		return true
	case strings.HasPrefix(line, "Removing:"):
		return true
	case strings.HasPrefix(line, "==> Summary"):
		return true
	}
	return false
}

// collapseNoOp detects brew's "already installed and up-to-date" no-op
// result and collapses the whole run to that single advisory line, marking
// any dropped follow-on hint lines (e.g. the "brew reinstall" suggestion).
func collapseNoOp(lines []string) (string, bool) {
	for idx, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Warning:") && alreadyInstalledRe.MatchString(line) {
			dropped := 0
			for _, rest := range lines[idx+1:] {
				if strings.TrimSpace(rest) != "" {
					dropped++
				}
			}
			if dropped > 0 {
				return fmt.Sprintf("%s …+%d", strings.TrimSpace(line), dropped), true
			}
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// captureCaveats collects a ==> Caveats section starting at lines[i] (the
// header itself) up to the next ==> header or EOF, capping the retained
// body at maxCaveatLines with a trailing elision marker for the remainder.
// It returns the block to emit verbatim and the index to resume from.
func captureCaveats(lines []string, i int) (block []string, next int) {
	block = append(block, lines[i])
	j := i + 1
	var body []string
	for j < len(lines) {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "==>") {
			break
		}
		body = append(body, lines[j])
		j++
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) > maxCaveatLines {
		block = append(block, body[:maxCaveatLines]...)
		block = append(block, fmt.Sprintf("…+%d lines", len(body)-maxCaveatLines))
	} else {
		block = append(block, body...)
	}
	return block, j
}

// aggressiveInstallUpgrade classifies `brew install`/`brew upgrade` output
// line by line: error/warning blocks and the final 🍺 result line always
// pass through verbatim; the ==> Caveats section is retained (capped);
// fetch/download/pour/cleanup progress noise collapses into a single …+N
// marker; everything else passes through unchanged. A pure no-op run
// ("already installed") collapses to one line. If nothing was transformed,
// the input is not recognizably brew install/upgrade output and the tier is
// inapplicable.
func aggressiveInstallUpgrade(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)
	lines := append(splitLines(rawStdout), splitLines(rawStderr)...)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if body, ok := collapseNoOp(lines); ok {
		return format.Rendered{Body: []byte(body), FoldStderr: len(rawStderr) > 0}, nil
	}

	var out []string
	transforms := 0
	inErrorBlock := false
	inAnalyticsBlock := false
	noiseRun := 0

	flushNoise := func() {
		if noiseRun > 0 {
			out = append(out, fmt.Sprintf("…+%d", noiseRun))
			transforms++
			noiseRun = 0
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			inErrorBlock = false
			inAnalyticsBlock = false
			continue
		}

		if inErrorBlock {
			if strings.HasPrefix(trimmed, "==>") {
				inErrorBlock = false
			} else {
				out = append(out, line)
				continue
			}
		}

		if inAnalyticsBlock {
			if strings.HasPrefix(trimmed, "==>") {
				inAnalyticsBlock = false
			} else {
				transforms++
				continue
			}
		}

		if isErrorSignal(line) {
			flushNoise()
			out = append(out, line)
			inErrorBlock = true
			continue
		}

		if trimmed == "==> Caveats" {
			flushNoise()
			block, next := captureCaveats(lines, i)
			out = append(out, block...)
			transforms++
			i = next - 1
			continue
		}

		if strings.HasPrefix(trimmed, "🍺") {
			flushNoise()
			out = append(out, line)
			continue
		}

		if strings.HasPrefix(line, "==> Homebrew has enabled") {
			flushNoise()
			transforms++
			inAnalyticsBlock = true
			continue
		}

		if isNoiseLine(line) {
			noiseRun++
			continue
		}

		flushNoise()
		out = append(out, line)
	}
	flushNoise()

	if transforms == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{
		Body:       []byte(strings.Join(out, "\n")),
		FoldStderr: len(rawStderr) > 0,
	}, nil
}
