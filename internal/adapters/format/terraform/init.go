package terraform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	initOKRE       = regexp.MustCompile(`has been successfully initialized`)
	initProviderRE = regexp.MustCompile(`^- (?:Installed|Using previously-installed) (\S+) (v\S+)`)
	initBackendRE  = regexp.MustCompile(`Successfully configured the backend "([^"]+)"`)
	initModuleRE   = regexp.MustCompile(`^- \S+ in \S+`)
)

// renderInit collapses a successful init to one line. Anything that is not a
// clean success (non-zero exit, Error:/Warning: text, an interactive prompt)
// declines, so the relaxed tier — which keeps every non-progress line —
// handles it.
func renderInit(in format.Input, program string) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("terraform: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("terraform: reading stderr: %w", err)
	}
	if in.ExitCode != 0 || len(stderr) > 0 || len(stdout) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	ok := false
	backend := ""
	modules := 0
	var providers []string
	seen := map[string]bool{}
	for _, raw := range splitLines(string(stdout)) {
		l := strings.TrimSpace(ansiRE.ReplaceAllString(raw, ""))
		unboxed := strings.TrimSpace(boxPrefix.ReplaceAllString(l, ""))
		if diagStartR.MatchString(unboxed) || strings.Contains(l, "Enter a value") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if initOKRE.MatchString(l) {
			ok = true
		}
		if m := initProviderRE.FindStringSubmatch(l); m != nil {
			p := m[1] + " " + m[2]
			if !seen[p] {
				seen[p] = true
				providers = append(providers, p)
			}
		} else if m := initBackendRE.FindStringSubmatch(l); m != nil {
			backend = m[1]
		} else if initModuleRE.MatchString(l) {
			modules++
		}
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var parts []string
	if backend != "" {
		parts = append(parts, "backend "+backend)
	}
	if len(providers) > 0 {
		parts = append(parts, "providers "+strings.Join(providers, ", "))
	}
	if modules > 0 {
		parts = append(parts, fmt.Sprintf("modules %d", modules))
	}
	body := program + " init: ok"
	if len(parts) > 0 {
		body += " (" + strings.Join(parts, "; ") + ")"
	}
	return format.Rendered{Body: []byte(body + "\n"), Elided: true}, nil
}
