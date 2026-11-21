// Package composer formats `composer install|update|require|remove`: the
// per-package Locking/Downloading/Installing lines collapse into one list per
// operation, progress bars and boilerplate are dropped, and every other line
// (conflict explanations, errors, warnings) is kept verbatim.
package composer

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `composer` output. It holds no state.
type Formatter struct{}

// New constructs a Formatter for `composer`.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `composer` invocation; dispatch happens inside.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "composer"} }

// packageOps are the commands whose output this package parses, with aliases.
var packageOps = map[string]bool{
	"install": true, "i": true, "update": true, "u": true, "upgrade": true,
	"require": true, "r": true, "remove": true, "rm": true, "uninstall": true,
	"reinstall": true,
}

var (
	opRE       = regexp.MustCompile(`^\s+- (Locking|Installing|Upgrading|Downgrading|Removing|Updating|Reinstalling|Downloading|Syncing) (\S+) \(([^)]*)\)(.*)$`)
	progressRE = regexp.MustCompile(`^\s*\d+/\d+ \[[=>\-]*\]\s+\d+%\s*$`)
	usageRE    = regexp.MustCompile(`^(?:install|update|require|remove|reinstall|i|u|r|rm|upgrade|uninstall) \[(?:-|<)`)
	noiseRE    = regexp.MustCompile(`^(?:Composer could not detect the root package|Loading composer repositories|Updating dependencies$|Writing lock file$|Installing dependencies from lock file|Generating autoload files|Verifying lock file contents|No security vulnerability advisories found|Use the ` + "`composer fund`" + `|\d+ packages? (?:you are using )?(?:is|are) looking for funding|\d+ package suggestions? w(?:as|ere) added)`)
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

func subcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			// Global options with a separate value.
			if (a == "-d" || a == "--working-dir") && i+1 < len(argv) {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

// Aggressive collapses an install/update/require/remove run.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if !packageOps[subcommand(in.Argv)] {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	// Composer writes nearly everything to stderr; read it first.
	lines := append(splitLines(ansiRE.ReplaceAllString(stderr, "")), splitLines(ansiRE.ReplaceAllString(stdout, ""))...)
	return render(lines)
}

// Relaxed drops progress bars and blank lines and collapses repeats.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if stdout == "" && stderr == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	for _, text := range []string{ansiRE.ReplaceAllString(stdout, ""), ansiRE.ReplaceAllString(stderr, "")} {
		lines := splitLines(text)
		for i := 0; i < len(lines); {
			l := strings.TrimRight(lines[i], " \t")
			if l == "" || progressRE.MatchString(l) {
				i++
				continue
			}
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], " \t") == l {
				j++
			}
			b.WriteString(l)
			if n := j - i; n > 1 {
				fmt.Fprintf(&b, " ×%d", n)
			}
			b.WriteByte('\n')
			i = j
		}
	}
	if b.Len() == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != ""}, nil
}

type pkg struct{ name, version string }

// render folds package operations and keeps all other lines. It declines when
// nothing in the output is recognisable composer text.
func render(lines []string) (format.Rendered, error) {
	verbs := []string{"Installing", "Upgrading", "Downgrading", "Reinstalling", "Updating", "Removing", "Syncing"}
	ops := map[string][]pkg{}
	seen := map[string]bool{}
	var locking []pkg
	var kept []string
	dropped := 0
	recognized := false

	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := opRE.FindStringSubmatch(l); m != nil {
			recognized = true
			verb := m[1]
			p := pkg{m[2], strings.ReplaceAll(m[3], " => ", "=>")}
			switch verb {
			case "Downloading":
				dropped++
			case "Locking":
				locking = append(locking, p)
			default:
				// Removals, upgrades and downgrades are announced twice:
				// once for the lock file and once for the vendor tree.
				if key := verb + " " + p.name + " " + p.version; !seen[key] {
					seen[key] = true
					ops[verb] = append(ops[verb], p)
				}
			}
			continue
		}
		switch {
		case progressRE.MatchString(l):
			dropped++
		case usageRE.MatchString(l):
			recognized = true
			dropped++
		case noiseRE.MatchString(l):
			recognized = true
			dropped++
		default:
			if strings.HasPrefix(l, "Package operations:") || strings.HasPrefix(l, "Lock file operations:") ||
				l == "Nothing to install, update or remove" || l == "Nothing to modify in lock file" {
				recognized = true
			}
			kept = append(kept, strings.TrimPrefix(l, "  "))
		}
	}
	if !recognized {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	any := false
	for _, v := range verbs {
		if ps := ops[v]; len(ps) > 0 {
			any = true
			writePkgs(&b, v, ps)
		}
	}
	if !any && len(locking) > 0 {
		writePkgs(&b, "Locking", locking)
	}
	for _, l := range kept {
		b.WriteString(l + "\n")
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "…+%d lines (progress, boilerplate)\n", dropped)
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: true, Elided: true}, nil
}

func writePkgs(b *strings.Builder, verb string, ps []pkg) {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.name + " " + p.version
	}
	fmt.Fprintf(b, "%s %d: %s\n", verb, len(ps), strings.Join(parts, ", "))
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("composer: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("composer: reading stderr: %w", err)
	}
	return string(o), string(e), nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
