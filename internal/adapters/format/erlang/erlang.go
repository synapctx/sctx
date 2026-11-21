// Package erlang formats `rebar3 compile`, `rebar3 eunit` and `rebar3 ct`.
// rebar3's "===> Verifying/Analyzing/Compiling" chatter and EUnit progress
// dots are dropped; each boxed compiler diagnostic (five lines of
// box-drawing around one message) becomes a single `file:line: message`
// line; EUnit and Common Test failures keep their expected/got detail and
// project stack frames but lose OTP-internal frames.
package erlang

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `rebar3` output. It holds no mutable state.
type Formatter struct{}

// New constructs the rebar3 formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `rebar3` invocation; the task is decided from argv.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "rebar3"} }

var (
	ansiRE   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	chatter  = regexp.MustCompile(`^===> (?:Verifying dependencies|Analyzing applications|Compiling \S+$|Fetching |Downloading |Uncompressing |Evaluating |Performing EUnit|Running Common Test|App \S+ is|Skipping |Dependencies:|Resolving)`)
	boxOpen  = regexp.MustCompile(`^\s*┌─ (\S+):$`)
	boxBar   = regexp.MustCompile(`^\s*│$`)
	boxSrc   = regexp.MustCompile(`^\s*(\d+) │ `)
	boxMsg   = regexp.MustCompile(`^\s*│\s*╰── (.*)$`)
	progress = regexp.MustCompile(`^[.F*!]+$`)
	finished = regexp.MustCompile(`^Finished in [\d.]+ seconds$`)
	// EUnit frames: "{eunit_proc,run_test,1," plus its "[{file,"eunit_proc.erl"},{line,N}]}," line.
	frameHead = regexp.MustCompile(`^\s+\{(?:eunit_\w+|test_server|ct_\w+),`)
	frameLoc  = regexp.MustCompile(`^\s+\[\{file,"(?:eunit_\w+|test_server|ct_\w+)\.erl"\},\{line,\d+\}\][\]},]*$`)
	ctDots    = regexp.MustCompile(`^%%% \S+: [.]*$`)
	ctAdvice  = regexp.MustCompile(`^(?:EXPERIMENTAL: Writing retry specification|\s+call rebar3 ct with '--retry'|Results written to)`)
)

// unescape maps rebar3's non-UTF-8 fallback spellings of the box glyphs back to
// the glyphs themselves, so one parser handles both locales.
var unescape = strings.NewReplacer(`\x{250C}`, "┌", `\x{2500}`, "─", `\x{2502}`, "│", `\x{2570}`, "╰")

func applicable(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "as" && i+1 < len(argv) {
			i++
			continue
		}
		switch a {
		case "compile", "eunit", "ct":
			return true
		}
		return false
	}
	return false
}

// Aggressive drops chatter, progress, internal frames and box furniture.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops chatter, progress dots and blank lines only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("erlang: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("erlang: reading stderr: %w", err)
	}
	text := unescape.Replace(ansiRE.ReplaceAllString(string(rawOut)+string(rawErr), ""))
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (rebar3 chatter, progress, diagnostic box furniture, OTP frames, blanks)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped int) {
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		switch {
		case strings.TrimSpace(l) == "", chatter.MatchString(l), progress.MatchString(l), finished.MatchString(l):
			dropped++
			continue
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		if m := boxOpen.FindStringSubmatch(l); m != nil && i+3 < len(lines) {
			src := boxSrc.FindStringSubmatch(lines[i+2])
			msg := boxMsg.FindStringSubmatch(strings.TrimRight(lines[i+3], " \t\r"))
			if boxBar.MatchString(lines[i+1]) && src != nil && msg != nil {
				kept = append(kept, fmt.Sprintf("%s:%s: %s", m[1], src[1], strings.TrimRight(msg[1], " ")))
				dropped += 3
				i += 3
				continue
			}
		}
		switch {
		case frameHead.MatchString(l) && i+1 < len(lines) && frameLoc.MatchString(lines[i+1]):
			dropped += 2
			i++
			continue
		case ctDots.MatchString(l), ctAdvice.MatchString(l):
			dropped++
			continue
		case strings.HasPrefix(strings.TrimSpace(l), "{test_server,"), strings.HasPrefix(strings.TrimSpace(l), "{ct_"):
			dropped++
			continue
		}
		kept = append(kept, l)
	}
	return kept, dropped
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
