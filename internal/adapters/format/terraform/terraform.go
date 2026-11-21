// Package terraform formats output from the `terraform` and `tofu` CLIs. The
// two share one implementation (All()), mirroring the pip/pip3 split in
// package pip. Subcommand dispatch happens inside the tiers: plan, apply and
// destroy drop refresh/lock/progress noise and collapse unchanged attributes;
// init collapses to a one-line summary; every other subcommand (validate, fmt,
// show, state, ...) declines so its output reaches the caller verbatim.
package terraform

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders terraform/tofu command output.
type Formatter struct {
	command string
}

// New returns the terraform formatter.
func New() format.Formatter { return &Formatter{command: "terraform"} }

// All returns the terraform and tofu formatters, sharing internals.
func All() []format.Formatter {
	return []format.Formatter{
		&Formatter{command: "terraform"},
		&Formatter{command: "tofu"},
	}
}

// Descriptor claims all invocations of the formatter's program name.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

// Aggressive compresses plan/apply/destroy and init output.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineOrHelp(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	switch subcommand(in.Argv) {
	case "plan", "apply", "destroy":
		return renderPlan(in, true)
	case "init":
		return renderInit(in, f.command)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed only drops known progress noise; it never collapses plan content.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if machineOrHelp(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	switch subcommand(in.Argv) {
	case "plan", "apply", "destroy", "init":
		return renderPlan(in, false)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// subcommand returns the first non-flag argument after argv[0]; global flags
// such as -chdir=DIR are attached with "=" and so never consume a value.
func subcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			continue
		}
		return argv[i]
	}
	return ""
}

// machineOrHelp reports flags whose output is not the human plan text.
func machineOrHelp(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	for _, a := range argv[1:] {
		if before, _, ok := strings.Cut(a, "="); ok {
			a = before
		}
		switch a {
		case "-json", "--json", "-help", "--help", "-h", "-version", "--version":
			return true
		}
	}
	return false
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
