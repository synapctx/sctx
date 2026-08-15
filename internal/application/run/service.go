// Package run orchestrates the wrap-a-command use case: resolve a formatter,
// execute the child, render its output through the tier chain, account
// tokens, and emit stats plus telemetry. The wrapped command's exit code and
// output are sacred — accounting and telemetry failures never affect them.
package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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
	"github.com/synapctx/sctx/internal/platform/rawcache"
	"github.com/synapctx/sctx/internal/platform/tokenizer"
)

type Options struct {
	Version   string
	ForceTier string
	RawCache  *rawcache.Cache
}

type Service struct {
	registry *Registry
	runner   domexec.Runner
	store    stats.Store       // may be nil (stats disabled)
	emitter  telemetry.Emitter // may be nil (telemetry disabled)
	sniff    format.Formatter  // generic fallback for unmatched commands, may be nil
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

	formatter, resolved := s.registry.ResolveByArgv(argv)
	formatterMatched := resolved
	if resolved {
		if classifier, ok := formatter.(interface{ Dedicated([]string) bool }); ok {
			formatterMatched = classifier.Dedicated(argv)
		}
	}
	if !resolved && s.sniff != nil {
		// EVERY unmatched command gets the generic formatter, not just the ones
		// whose output starts with `{`. The JSON-only gate that used to stand here
		// is why the unmatched path saved exactly zero tokens across 179 runs and
		// 50,124 raw tokens: anything printing repetitive TEXT paid full price
		// while a general line collapser sat unreachable inside `read`.
		//
		// Safe unconditionally because both of the generic tiers DETECT before they
		// act and decline otherwise, and the tier chain degrades to verbatim — so
		// the worst case for a command nobody has ever formatted is exactly what
		// happens today.
		formatter = s.sniff

		// formatterMatched stays FALSE. The generic formatter is a safety net, not
		// coverage, and telemetry must keep telling the two apart or the
		// coverage-gap meter goes blind: the old JSON sniffer reported itself as
		// matched, which counted a sniffed command as a covered one.
	}

	in := format.Input{
		Argv:     argv,
		Command:  CommandKey(argv),
		Stdout:   bytes.NewReader(raw),
		Stderr:   bytes.NewReader(rawStderr),
		ExitCode: outcome.ExitCode,
		Duration: outcome.Duration,
	}
	result := renderChain(ctx, formatter, in, raw, rawStderr, s.opts.ForceTier)

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
	if result.Elided && s.opts.RawCache != nil {
		if entry, err := s.opts.RawCache.Store(raw, rawStderr); err == nil {
			hint := fmt.Sprintf("sctx: raw output: %s (%s)\n", entry.Path, s.opts.RawCache.TTL)
			if n, err := s.stderr.Write([]byte(hint)); err == nil {
				emittedStderr += n
			}
		}
	}

	s.account(ctx, argv, formatter, formatterMatched, outcome, result, int64(len(raw)+len(rawStderr)), int64(len(result.Body)+emittedStderr))
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
	formatterKind := telemetry.FormatterKindNone
	if formatterMatched {
		formatterKind = telemetry.FormatterKindDedicated
	} else if formatter != nil {
		formatterKind = telemetry.FormatterKindGeneric
	}
	outputReduced := saved > 0
	declineReason := ""
	if !outputReduced {
		switch {
		case rawBytes == 0:
			declineReason = telemetry.DeclineSilentCommand
		case s.opts.ForceTier == string(format.TierVerbatim) || s.opts.ForceTier == "off":
			declineReason = telemetry.DeclineExplicitBypass
		case result.Tier != format.TierVerbatim:
			declineReason = telemetry.DeclineNoNetSaving
		case formatterKind == telemetry.FormatterKindDedicated:
			declineReason = telemetry.DeclineUnrecognizedOutput
		case formatterKind == telemetry.FormatterKindGeneric:
			declineReason = telemetry.DeclineArbitraryOutput
		default:
			declineReason = telemetry.DeclineUnsupportedCommand
		}
	}

	id, err := ulid.New()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	formatterName := "verbatim"
	if !formatterMatched && formatter != nil {
		formatterName = "(generic)"
	} else if formatter != nil {
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
			FormatterKind:    formatterKind,
			OutputReduced:    outputReduced,
			DeclineReason:    declineReason,
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
// program's basename plus its operation (e.g. "go test", "git status").
// Git's global options are scanned before selecting that operation so paths
// passed through -C/--git-dir never enter telemetry keys.
func deriveProgram(argv []string) string {
	return progkey.FromArgv(argv)
}

// looksLikeJSON is deliberately GONE. It gated the fallback on stdout starting
// with `{` or `[`, which meant a command printing repetitive text got no
// formatter at all. The generic formatter's own tiers now decide applicability
// by parsing, which is both stricter (encoding/json, not a first-byte guess) and
// wider (text gets the line collapser).
