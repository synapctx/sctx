// Package scala formats `sbt` batch output for compile and test tasks. sbt's
// startup, resolution and "compiling N sources" chatter, passing test rows and
// framework stack frames are dropped; compiler diagnostics keep their location
// and message but lose the echoed source line and caret, failing tests keep
// their assertion text, and every run keeps its [success]/[error] trailer.
package scala

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders sbt output. It holds no mutable state.
type Formatter struct{}

// New constructs the sbt formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `sbt` invocation; applicability is decided from argv.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "sbt"} }

var (
	ansiRE  = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	levelRE = regexp.MustCompile(`^\[(info|warn|error|success|debug)\] ?(.*)$`)
	noiseRE = regexp.MustCompile(`^(?:welcome to sbt|loading (?:project definition|settings|global|global plugins)|set current project|Executing in batch mode|resolving key references|compiling \d+ |done compiling|Run completed in|Total number of tests run|Suites: completed|Resolving |Fetching artifacts|downloading |downloaded |Updating$|Done updating|Non-compiled module|Compilation completed|Passed: Total|\[success\] created output)`)
	// Scala 3 diagnostic furniture: numbered source line, caret row, blank gutter.
	srcRE     = regexp.MustCompile(`^\d+ \|`)
	caret3RE  = regexp.MustCompile(`^\s+\|\s+\^+\s*$`)
	gutterRE  = regexp.MustCompile(`^\s+\|\s*$`)
	explainRE = regexp.MustCompile("longer explanation available when compiling with `-explain`")
	caret2RE  = regexp.MustCompile(`^\s*\^+\s*$`)
	// Test framework furniture.
	passRE      = regexp.MustCompile(`^\s*(?:- .*|\+ .*|✓ .*)$`)
	failedRowRE = regexp.MustCompile(`\*\*\* FAILED \*\*\*|^\s*==> X |\*\*\* ABORTED \*\*\*`)
	suiteRE     = regexp.MustCompile(`^\w[\w.$]*:$`)
	frameRE     = regexp.MustCompile(`^\s+at (?:org\.scalatest|munit\.|scala\.|java\.|jdk\.|sbt\.|org\.junit|sun\.)`)
	elideRE     = regexp.MustCompile(`^\s+\.\.\.$`)
)

// supported reports whether argv names only tasks whose output this package
// understands. A bare `sbt` opens an interactive shell; `~task` watches
// forever; run/console hand the terminal to the user's program.
func supported(argv []string) bool {
	seen := false
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			if strings.HasPrefix(a, "-Dsbt.log.noformat") || strings.HasPrefix(a, "-Dsbt.color") || a == "-batch" || a == "--batch" {
				continue
			}
			if a == "--client" || a == "-client" || strings.HasPrefix(a, "--json") {
				return false
			}
			continue
		}
		cmd := strings.Fields(strings.Trim(a, `"'`))
		if len(cmd) == 0 {
			continue
		}
		name := cmd[0]
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		name = strings.TrimPrefix(name, "Test:")
		switch {
		case strings.HasPrefix(name, "~"), name == "shell", name == "console", name == "consoleQuick",
			name == "run", name == "runMain", name == "new", name == "client", name == "show", name == "inspect",
			name == "bloopInstall":
			return false
		case strings.HasPrefix(name, "compile"), strings.HasPrefix(name, "test"), strings.HasPrefix(name, "update"),
			name == "package", name == "clean", name == "publishLocal", name == "scalafmtCheck", name == "scalafmtCheckAll",
			name == "it:test", name == "coverage", name == "coverageReport", name == "doc":
			seen = true
		default:
			return false
		}
	}
	return seen
}

// Aggressive drops chatter, passing rows, excerpts and framework frames.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only startup/resolution chatter and Scala diagnostic echoes.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !supported(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("scala: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("scala: reading stderr: %w", err)
	}
	text := ansiRE.ReplaceAllString(string(rawOut)+string(rawErr), "")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (sbt chatter, passing tests, source echoes, framework frames)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped int) {
	suite := ""      // suite header held until a kept row follows it
	var failing bool // inside a failing test's detail block
	for _, raw := range lines {
		lvl, body := "", strings.TrimRight(raw, " \t")
		if m := levelRE.FindStringSubmatch(body); m != nil {
			lvl, body = m[1], strings.TrimRight(m[2], " \t")
		}
		trim := strings.TrimSpace(body)
		switch {
		case trim == "" && lvl != "":
			dropped++
			continue
		case lvl == "info" && noiseRE.MatchString(body):
			dropped++
			continue
		}
		// Scala 3 / Scala 2 echoes, on warn/error lines.
		if lvl == "warn" || lvl == "error" {
			switch {
			case srcRE.MatchString(body), caret3RE.MatchString(body), gutterRE.MatchString(body), explainRE.MatchString(body):
				dropped++
				continue
			case caret2RE.MatchString(body):
				// Scala 2: the source line printed just before the caret is dropped too.
				if n := len(kept); n > 0 && strings.HasPrefix(kept[n-1], "[") {
					kept = kept[:n-1]
					dropped++
				}
				dropped++
				continue
			}
			kept = append(kept, fmt.Sprintf("[%s] %s", lvl, body))
			continue
		}
		if lvl == "info" {
			if !aggressive {
				kept = append(kept, body)
				continue
			}
			switch {
			case suiteRE.MatchString(body):
				suite = body
				dropped++ // un-dropped if a failing row follows
				failing = false
				continue
			case failedRowRE.MatchString(body):
				if suite != "" {
					kept = append(kept, suite)
					dropped--
					suite = ""
				}
				failing = true
				kept = append(kept, body)
				continue
			case passRE.MatchString(body) && !failing:
				dropped++
				continue
			case passRE.MatchString(body):
				failing = false
				dropped++
				continue
			case frameRE.MatchString(body), elideRE.MatchString(body):
				dropped++
				continue
			}
			kept = append(kept, body)
			continue
		}
		// Unprefixed lines are munit rows and test output (ANSI already stripped).
		if aggressive {
			switch {
			case suiteRE.MatchString(body):
				suite = body
				dropped++
				failing = false
				continue
			case failedRowRE.MatchString(body):
				if suite != "" {
					kept = append(kept, suite)
					dropped--
					suite = ""
				}
				failing = true
				kept = append(kept, body)
				continue
			case passRE.MatchString(body) && !failing:
				dropped++
				continue
			case frameRE.MatchString(body):
				dropped++
				continue
			}
		}
		if lvl == "success" {
			failing = false
			body = "[success] " + body
		}
		kept = append(kept, body)
	}
	return kept, dropped
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
