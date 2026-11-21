// Package awscli formats `aws` CLI output that is not JSON. JSON documents are
// deliberately left to the lossless jsoncompact fallback (which packs arrays of
// objects with a self-verifying codec): a lossy projection of `aws ec2
// describe-instances` would hide fields the agent asked for. Failures decline
// on every tier — an error is a handful of lines and every one of them is
// signal.
//
// What this package adds on top of the fallback:
//
//   - `--output table` boxes (~half borders and padding) become one line per
//     record;
//   - `aws <svc> help` man pages lose their overstrike bold/underline
//     (X\bX) that triples every heading;
//   - `s3 cp|mv|rm|sync` operation lines are grouped by verb and common
//     source/destination prefix, and byte-progress ticker lines are dropped;
//   - very long `s3 ls` listings keep their head and tail with a counted marker.
package awscli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders aws CLI output.
type Formatter struct{}

// New returns the aws formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims every aws invocation.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "aws"} }

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// Aggressive applies every transformation, including the s3 ls cap.
func (*Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed applies only the lossless transformations.
func (*Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

// words returns argv's non-flag words after argv[0]. Flag values are skipped
// for the handful of global flags that take a separate value.
func words(argv []string) []string {
	var w []string
	skip := false
	for _, a := range argv[1:] {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			switch a {
			case "--profile", "--region", "--output", "--query", "--endpoint-url", "--cli-read-timeout", "--cli-connect-timeout", "--ca-bundle", "--color":
				skip = true
			}
			continue
		}
		w = append(w, a)
	}
	return w
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	w := words(in.Argv)
	if len(w) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if in.Stdout == nil {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("awscli: reading stdout: %w", err)
	}
	text := string(raw)
	if text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var body string
	elided := false
	switch {
	case w[len(w)-1] == "help":
		body = renderHelp(text)
	case w[0] == "s3" && len(w) >= 2:
		switch w[1] {
		case "ls":
			if !aggressive {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			body, elided = renderLs(text)
		case "cp", "mv", "rm", "sync":
			body = renderTransfer(text)
		}
	}
	if body == "" && isTable(text) {
		body = renderTable(text)
	}
	if body == "" || body == text {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return format.Rendered{Body: []byte(body), Elided: elided}, nil
}
