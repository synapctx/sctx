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
		return format.Rendered{
			Body:       []byte(strings.Join(errorLines, "\n")),
			FoldStderr: len(rawErr) > 0,
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

	noiseTotal := satisfied + downloading + collecting + building
	if noiseTotal > 0 {
		fmt.Fprintf(&b, "\n…+%d (%d satisfied, %d downloaded, %d build steps)", noiseTotal, satisfied, downloading, collecting+building)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
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
