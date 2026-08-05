// Package run orchestrates the wrap-a-command use case: resolve a formatter,
// execute the child, render its output through the tier chain, account
// tokens, and emit stats plus telemetry. The wrapped command's exit code and
// output are sacred — accounting and telemetry failures never affect them.
package run

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/synapctx/sctx/internal/platform/progkey"

	"github.com/cloudresty/ulid"

	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
	"github.com/synapctx/sctx/internal/platform/tokenizer"
)

type Options struct {
	Version   string
	ForceTier string
}

type Service struct {
	registry *Registry
	runner   domexec.Runner
	store    stats.Store       // may be nil (stats disabled)
	emitter  telemetry.Emitter // may be nil (telemetry disabled)
	sniff    format.Formatter  // JSON fallback for unmatched commands, may be nil
	stdout   io.Writer
	stderr   io.Writer
	opts     Options

	repoOnce sync.Once
	repoName string // "org/repo", detected lazily on first Execute
}

func NewService(registry *Registry, runner domexec.Runner, store stats.Store, emitter telemetry.Emitter, sniff format.Formatter, stdout, stderr io.Writer, opts Options) *Service {
	return &Service{
		registry: registry,
		runner:   runner,
		store:    store,
		emitter:  emitter,
		sniff:    sniff,
		stdout:   stdout,
		stderr:   stderr,
		opts:     opts,
	}
}

// Execute runs argv, renders its output, and returns the exit code sctx must
// exit with. The returned error reports sctx-internal failures (command not
// startable); the wrapped command failing is not an error.
func (s *Service) Execute(ctx context.Context, argv []string) (int, error) {
	outcome, runErr := s.runner.Run(ctx, domexec.Command{Argv: argv})
	if runErr != nil {
		return outcome.ExitCode, runErr
	}
	defer outcome.Stdout.Close()
	defer outcome.Stderr.Close()

	raw, err := readAll(outcome.Stdout)
	if err != nil {
		return outcome.ExitCode, err
	}
	rawStderr, err := readAll(outcome.Stderr)
	if err != nil {
		return outcome.ExitCode, err
	}

	formatter, matched := s.registry.ResolveByArgv(argv)
	if !matched && s.sniff != nil && looksLikeJSON(raw) {
		formatter = s.sniff
		matched = true // the JSON sniffer still compresses; only "no formatter, no sniffer" counts as unmatched.
	}

	in := format.Input{
		Argv:     argv,
		Command:  CommandKey(argv),
		Stdout:   bytes.NewReader(raw),
		Stderr:   bytes.NewReader(rawStderr),
		ExitCode: outcome.ExitCode,
		Duration: outcome.Duration,
	}
	result := renderChain(ctx, formatter, in, raw, s.opts.ForceTier)

	if _, err := s.stdout.Write(result.Body); err != nil {
		return outcome.ExitCode, err
	}
	emittedStderr := 0
	if !result.FoldStderr && len(rawStderr) > 0 {
		if _, err := s.stderr.Write(rawStderr); err != nil {
			return outcome.ExitCode, err
		}
		emittedStderr = len(rawStderr)
	}

	s.account(ctx, argv, formatter, matched, outcome, result, int64(len(raw)+len(rawStderr)), int64(len(result.Body)+emittedStderr))
	return outcome.ExitCode, nil
}

// account records local stats and spools a telemetry event. Best-effort by
// design: failures are ignored so they can never affect the wrapped command.
func (s *Service) account(ctx context.Context, argv []string, formatter format.Formatter, formatterMatched bool, outcome domexec.Outcome, result RenderResult, rawBytes, outBytes int64) {
	rawTokens := tokenizer.Estimate(rawBytes)
	outTokens := tokenizer.Estimate(outBytes)
	saved := rawTokens - outTokens
	if saved < 0 {
		saved = 0
	}

	id, err := ulid.New()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	formatterName := "verbatim"
	if formatter != nil {
		formatterName = formatter.Descriptor().Command
	}

	if s.store != nil {
		_ = s.store.Record(ctx, stats.Run{
			ID:          id,
			At:          now,
			Command:     CommandKey(argv),
			Argv:        strings.Join(argv, " "),
			Formatter:   formatterName,
			Tier:        string(result.Tier),
			RawBytes:    rawBytes,
			RawTokens:   rawTokens,
			OutTokens:   outTokens,
			SavedTokens: saved,
			ExitCode:    outcome.ExitCode,
			DurationMS:  outcome.Duration.Milliseconds(),
			Anomaly:     result.Anomaly,
			Repository:  s.repository(),
		})
	}

	if s.emitter != nil {
		s.emitter.Emit(telemetry.Event{
			ID:               id,
			Kind:             telemetry.KindExecSavings,
			Tool:             "sctx",
			Version:          s.opts.Version,
			RepositoryName:   s.repository(),
			Command:          CommandKey(argv),
			Program:          deriveProgram(argv),
			Tier:             string(result.Tier),
			RawTokens:        rawTokens,
			OutTokens:        outTokens,
			SavedTokens:      saved,
			ExitCode:         outcome.ExitCode,
			DurationMS:       outcome.Duration.Milliseconds(),
			FormatterMatched: formatterMatched,
			At:               now,
		})
	}
}

// repository lazily detects the "org/repo" of the current working
// directory's git repository, once per Service (i.e. once per Execute
// process lifetime). Detection failures (no git, no origin) resolve to "".
func (s *Service) repository() string {
	s.repoOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			return
		}
		s.repoName = gitrepo.Detect(wd)
	})
	return s.repoName
}

func readAll(s domexec.Spill) ([]byte, error) {
	r, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// deriveProgram returns the stable aggregation key used by telemetry: the
// program's basename plus its first non-flag argument (e.g. "go test",
// "terraform plan"). Mechanical by design — unlike CommandKey it doesn't
// consult a per-program subcommand table, so it stays cheap to compute for
// every command including ones sctx doesn't format.
func deriveProgram(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	var next string
	if len(argv) > 1 {
		next = argv[1]
	}
	return progkey.Key(filepath.Base(argv[0]), next)
}

// looksLikeJSON reports whether the first non-whitespace byte starts a JSON
// document.
func looksLikeJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}
