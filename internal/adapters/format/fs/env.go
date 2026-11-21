package fs

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// envFormatter renders `env` / `printenv` listings. Every variable NAME is
// kept in its original order. Long values are cut with an explicit "…+N chars"
// marker and PATH-like lists keep their first entries plus a count, except for
// secret-looking names, whose values are emitted untouched so the redaction
// layer that runs after rendering sees exactly the bytes the raw output had.
type envFormatter struct{ command string }

func (f *envFormatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

const (
	// envValueCap is the longest non-secret value printed in full.
	envValueCap = 120
	// envValueHead is how much of an over-long value survives.
	envValueHead = 80
	// envPathEntriesShown is how many entries of a PATH-like list survive.
	envPathEntriesShown = 4
	// envMinLines guards against compressing a handful of variables.
	envMinLines = 8
)

var (
	envLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	// envSecretNameRe flags names whose values must pass through unmodified.
	envSecretNameRe = regexp.MustCompile(`(?i)(token|secret|passw|pwd|credential|auth|cookie|session|private|api[_-]?key|access[_-]?key|_key$|^key$|dsn|signature|bearer|jwt|cert)`)
)

func (f *envFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if len(in.Argv) > 1 {
		// env with options/operands (-0, -u, NAME=val cmd, printenv NAME) is
		// either NUL-separated, a command wrapper or a single value.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(raw)
	if len(lines) < envMinLines {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := make([]string, 0, len(lines))
	vars, cut := 0, 0
	for _, l := range lines {
		m := envLineRe.FindStringSubmatch(l)
		if m == nil {
			// A continuation line of a multi-line value: keep verbatim.
			out = append(out, l)
			continue
		}
		vars++
		line := renderEnvVar(m[1], m[2])
		if line != l {
			cut++
		}
		out = append(out, line)
	}
	if vars == 0 || cut == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := strings.Join(out, "\n")
	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d vars (%d values shortened)", vars, cut), Elided: true}, nil
}

// renderEnvVar shortens one NAME=value line when its value is long. Secret-looking
// names are returned untouched.
func renderEnvVar(name, value string) string {
	line := name + "=" + value
	if len(value) <= envValueCap || envSecretNameRe.MatchString(name) {
		return line
	}
	if parts := strings.Split(value, ":"); len(parts) > envPathEntriesShown+1 {
		return fmt.Sprintf("%s=%s:…+%d entries (%d chars)", name, strings.Join(parts[:envPathEntriesShown], ":"), len(parts)-envPathEntriesShown, len(value))
	}
	return fmt.Sprintf("%s=%s…+%d chars", name, value[:cutBoundary(value, envValueHead)], len(value)-cutBoundary(value, envValueHead))
}

// cutBoundary returns n adjusted down so the cut does not split a UTF-8 rune.
func cutBoundary(s string, n int) int {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return n
}

// Relaxed declines: the generic line cap would drop whole variables, and an
// environment listing has no repeated-line redundancy to remove.
func (f *envFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
