// Package ansible formats `ansible-playbook` output. A playbook run prints a
// TASK banner and one "ok:"/"skipping:" line per host per task, which on a
// healthy run is nearly all of the output. The aggressive tier drops the
// no-news results, drops the banner of any task that has none left, and keeps
// everything that is news: changed, failed, fatal, unreachable, rescued,
// ignored, debug bodies, [ERROR]/[WARNING] blocks and the PLAY RECAP. Counts of
// what was dropped lead the render.
package ansible

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders ansible-playbook output.
type Formatter struct{}

// New returns the ansible-playbook formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims ansible-playbook invocations.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "ansible-playbook"} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	bannerRE   = regexp.MustCompile(`^(PLAY|TASK|RUNNING HANDLER|PLAY RECAP)( \[.*\])? \*+$`)
	taskRE     = regexp.MustCompile(`^(TASK|RUNNING HANDLER) \[`)
	okPlainRE  = regexp.MustCompile(`^ok: \[[^\]]+\](?: => \(item=.*\))?$`)
	okBodyRE   = regexp.MustCompile(`^ok: \[[^\]]+\](?: => \(item=.*\))? => \{$`)
	skipRE     = regexp.MustCompile(`^skipping: \[`)
	trailStars = regexp.MustCompile(` \*+$`)
)

// Aggressive drops no-news results and the banners of tasks left empty.
func (*Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed only strips banner fill, blank lines and skipped results.
func (*Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func machineOrListing(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "--list-tasks", "--list-tags", "--list-hosts", "--syntax-check", "--version", "--help", "-h":
			return true
		}
	}
	return false
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || machineOrListing(in.Argv) || in.Stdout == nil {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("ansible: reading stdout: %w", err)
	}
	var stderr []byte
	if in.Stderr != nil {
		if stderr, err = io.ReadAll(in.Stderr); err != nil {
			return format.Rendered{}, fmt.Errorf("ansible: reading stderr: %w", err)
		}
	}
	text := ansiRE.ReplaceAllString(string(raw), "")
	if !strings.Contains(text, "PLAY [") && !strings.Contains(text, "PLAY RECAP") {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []string
	counts := map[string]int{}
	taskHdr := ""
	var taskKept []string
	flush := func() {
		if taskHdr == "" {
			return
		}
		if len(taskKept) > 0 || !aggressive {
			out = append(out, taskHdr)
			out = append(out, taskKept...)
		} else {
			counts["empty tasks"]++
		}
		taskHdr, taskKept = "", nil
	}
	emit := func(l string) {
		if taskHdr != "" {
			taskKept = append(taskKept, l)
		} else {
			out = append(out, l)
		}
	}

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \r")
		switch {
		case bannerRE.MatchString(l):
			flush()
			h := trailStars.ReplaceAllString(l, "")
			if taskRE.MatchString(h) {
				taskHdr = h
			} else {
				out = append(out, h)
			}
		case l == "":
			counts["blank"]++
		case skipRE.MatchString(l):
			counts["skipping"]++
		case aggressive && okPlainRE.MatchString(l):
			counts["ok"]++
		case okBodyRE.MatchString(l):
			// "ok: [h] => {" ... "}": a debug/-v body. Keep it, and fold a
			// one-key body onto the header line.
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], " \r") != "}" {
				j++
			}
			if j < len(lines) && j-i == 2 {
				emit(l + strings.TrimSpace(lines[i+1]) + "}")
			} else {
				for k := i; k <= j && k < len(lines); k++ {
					emit(strings.TrimRight(lines[k], " \r"))
				}
			}
			i = j
		default:
			emit(l)
		}
	}
	flush()

	n := 0
	var parts []string
	for _, k := range []string{"ok", "skipping", "empty tasks", "blank"} {
		if counts[k] > 0 {
			n += counts[k]
			parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
		}
	}
	if n == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	fmt.Fprintf(&b, "…+%d lines (%s)\n", n, strings.Join(parts, ", "))
	b.WriteString(strings.Join(out, "\n"))
	b.WriteByte('\n')
	fold := false
	if len(stderr) > 0 {
		b.Write(stderr)
		if stderr[len(stderr)-1] != '\n' {
			b.WriteByte('\n')
		}
		fold = true
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: true}, nil
}
