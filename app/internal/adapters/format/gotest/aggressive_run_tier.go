package gotest

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveRun implements the aggressive tier for `go run`. Its stdout is
// the *program's own output*, which sctx must not mangle — this tier only
// adds value when the build failed before the program ever ran, in which
// case it keeps the compiler/vet diagnostics and drops nothing else. When
// the program appears to have actually executed, this tier declines so a
// later tier (or verbatim) preserves the program's output byte-for-byte.
func aggressiveRun(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if in.ExitCode == 0 || !looksLikeCompileFailure(string(stderr)) {
		// Either the run succeeded (program output is sacred, don't
		// compress it) or it failed for a reason other than a build
		// error we can confidently summarize (e.g. a runtime panic,
		// which is itself the value the caller wants intact).
		return format.Rendered{}, format.ErrTierInapplicable
	}

	// Compile failure: `go run` never reaches program output, so stdout is
	// empty/irrelevant and stderr carries the full diagnostic. Dedupe only;
	// every diagnostic line is error signal and must be kept.
	lines := dedupeLines(splitLines(string(stderr)))
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body), FoldStderr: true}, nil
}

// looksLikeCompileFailure reports whether stderr carries the package-header
// or file:line:col diagnostics `go build`/`go run` emit when compilation
// fails, as opposed to a runtime panic or the program's own stderr output.
func looksLikeCompileFailure(stderr string) bool {
	lines := splitLines(stderr)
	for _, l := range lines {
		if strings.HasPrefix(l, "# ") {
			return true
		}
		if isFileLineDiagnostic(l) {
			return true
		}
	}
	return false
}

// isFileLineDiagnostic reports whether l matches the compiler's
// "path/file.go:LINE:COL: message" shape.
func isFileLineDiagnostic(l string) bool {
	idx := strings.Index(l, ".go:")
	if idx <= 0 {
		return false
	}
	rest := l[idx+len(".go:"):]
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return false
	}
	linePart := rest[:colon]
	for _, r := range linePart {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
