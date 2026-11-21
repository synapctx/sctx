package pip

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxInstalledPackages caps how many newly-installed package names are
// listed verbatim on the "Successfully installed" line before eliding the
// rest behind a "…+N" marker.
const maxInstalledPackages = 30

// A failed install keeps every non-noise line up to maxFailureLines; beyond
// that the head and tail survive and the middle is counted.
const (
	maxFailureLines = 60
	failureHead     = 20
	failureTail     = 30
)

// aggressiveInstall collapses `pip install` output, which is dominated by
// progress noise (Collecting/Downloading/Requirement already satisfied/
// build steps), down to: the Successfully-installed summary (capped), a
// noise-count marker, and — on failure — the ERROR lines in full.
func aggressiveInstall(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	outLines := splitLines(rawOut)
	errLines := splitLines(rawErr)
	if len(outLines) == 0 && len(errLines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	// errorLines keeps every non-noise line in order — ERROR lines, but also
	// the build backend's output ("error: subprocess-exited-with-error", the
	// compiler's own messages) that explains a failed wheel build.
	var errorLines []string
	var installedLine, builtLine string
	satisfied, downloading, collecting, building := 0, 0, 0, 0

	classify := func(l string) {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "ERROR"):
			errorLines = append(errorLines, t)
		case strings.HasPrefix(t, "Successfully installed"):
			installedLine = t
		case strings.HasPrefix(t, "Successfully built"):
			builtLine = t
		case strings.HasPrefix(t, "Requirement already satisfied"):
			satisfied++
		case strings.HasPrefix(t, "Downloading"), strings.HasPrefix(t, "Using cached"):
			downloading++
		case strings.HasPrefix(t, "Collecting"):
			collecting++
		case strings.HasPrefix(t, "Building wheel"),
			strings.HasPrefix(t, "Preparing metadata"),
			strings.HasPrefix(t, "Installing build dependencies"),
			strings.HasPrefix(t, "Getting requirements to build wheel"):
			building++
		case t == "", strings.HasPrefix(t, "Installing collected packages"),
			strings.HasPrefix(t, "Attempting uninstall"), strings.HasPrefix(t, "Found existing installation"),
			strings.HasPrefix(t, "Uninstalling "), strings.HasPrefix(t, "Successfully uninstalled"),
			strings.HasPrefix(t, "Obtaining "), strings.HasPrefix(t, "Looking in "), strings.HasPrefix(t, "Created wheel"),
			strings.HasPrefix(t, "Stored in directory"), strings.HasPrefix(t, "━"), strings.HasPrefix(t, "Preparing editable"):
			// progress chatter: nothing to keep
		default:
			errorLines = append(errorLines, strings.TrimRight(l, " \t\r"))
		}
	}
	for _, l := range outLines {
		classify(l)
	}
	for _, l := range errLines {
		classify(l)
	}

	if in.ExitCode != 0 {
		if len(errorLines) == 0 {
			// No recognizable error signal; let relaxed filtering preserve
			// whatever stderr/stdout actually says.
			return format.Rendered{}, format.ErrTierInapplicable
		}
		elided := false
		if len(errorLines) > maxFailureLines {
			more := len(errorLines) - failureHead - failureTail
			kept := append([]string{}, errorLines[:failureHead]...)
			kept = append(kept, fmt.Sprintf("…+%d lines", more))
			errorLines = append(kept, errorLines[len(errorLines)-failureTail:]...)
			elided = true
		}
		return format.Rendered{
			Body:       []byte(strings.Join(errorLines, "\n")),
			FoldStderr: len(rawErr) > 0,
			Elided:     elided,
		}, nil
	}

	var b strings.Builder
	switch {
	case installedLine != "":
		b.WriteString(capPackageLine(installedLine, maxInstalledPackages))
	case builtLine != "":
		b.WriteString(builtLine)
	default:
		b.WriteString("pip install: no changes")
	}

	for _, l := range errorLines {
		b.WriteByte('\n')
		b.WriteString(l)
	}

	noiseTotal := satisfied + downloading + collecting + building
	if noiseTotal > 0 {
		fmt.Fprintf(&b, "\n…+%d (%d satisfied, %d downloaded, %d build steps)", noiseTotal, satisfied, downloading, collecting+building)
	}

	// Stderr lines are part of Body above, so stderr must be folded or the
	// caller would print them a second time.
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(rawErr) > 0}, nil
}

// capPackageLine truncates a "Successfully installed a-1 b-2 ..." line to
// max package entries, appending a visible "…+N" marker for the rest.
func capPackageLine(line string, max int) string {
	const prefix = "Successfully installed "
	if !strings.HasPrefix(line, prefix) {
		return line
	}
	pkgs := strings.Fields(strings.TrimPrefix(line, prefix))
	if len(pkgs) <= max {
		return line
	}
	kept := pkgs[:max]
	return prefix + strings.Join(kept, " ") + fmt.Sprintf(" …+%d", len(pkgs)-max)
}
