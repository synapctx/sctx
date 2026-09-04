package main

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/synapctx/sctx/internal/adapters/exec/osproc"
	"github.com/synapctx/sctx/internal/adapters/format/generic"
	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/application/run"
	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/tokenizer"
)

// benchSchemaVersion is the stable shape of `sctx bench --format json`.
// Bump it, and only it, when a field is added or removed.
const benchSchemaVersion = 1

const benchUsage = `usage: sctx bench [--name <repo>] [--format text|json] [--verbose]`

// benchGoCommands is the fixed, documented Go command set: `sctx bench`
// detects Go by a go.mod at the repository root and runs exactly these, in
// this order, never more and never fewer, so a published run is
// reproducible by anyone who reads this file.
var benchGoCommands = [][]string{
	{"go", "build", "./..."},
	{"go", "vet", "./..."},
	{"go", "test", "./..."},
	{"git", "status"},
	{"git", "log", "--oneline", "-n", "50"},
	{"git", "diff", "HEAD~1"},
	{"grep", "-rn", "func ", "--include=*.go", "."},
	{"ls", "-la"},
	{"find", ".", "-name", "*.go"},
}

// benchGenericCommands runs when no supported language marker is found. It
// deliberately stays language-agnostic: no build/test/lint step, since
// there is nothing to safely assume about how to invoke one.
var benchGenericCommands = [][]string{
	{"git", "status"},
	{"git", "log", "--oneline", "-n", "50"},
	{"git", "diff", "HEAD~1"},
	{"ls", "-la"},
	{"find", ".", "-maxdepth", "2"},
}

// benchCommandsFor picks the fixed command set for cwd by the files present
// there — today, a single marker: go.mod means Go. Never a heuristic on file
// contents, and never on argv the caller supplies.
func benchCommandsFor(cwd string) (language string, cmds [][]string) {
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return "go", benchGoCommands
	}
	return "generic", benchGenericCommands
}

// benchRow is one command's raw-vs-rendered comparison.
type benchRow struct {
	// Command is the normalized program key (run.CommandKey): a bare program
	// or "program subcommand", never a full argv and never a path — printed
	// unconditionally.
	Command string `json:"command"`
	// Argv is the full command line, printed and JSON-encoded only with
	// --verbose: it is the one field that can carry a path (e.g. "." or a
	// file argument), so it stays opt-in.
	Argv         []string `json:"argv,omitempty"`
	RawTokens    int64    `json:"rawTokens"`
	RenderTokens int64    `json:"renderedTokens"`
	SavedPct     float64  `json:"savedPct"`
	Tier         string   `json:"tier"`
	FormatterKey string   `json:"formatter"`
	ExitCode     int      `json:"exitCode"`
}

// benchResult is the stable `sctx bench --format json` document.
type benchResult struct {
	SchemaVersion int          `json:"schemaVersion"`
	Repository    string       `json:"repository"` // "" -> printed as "(unnamed)" in text mode
	Language      string       `json:"language"`   // "go" | "generic"
	Machine       benchMachine `json:"machine"`
	Estimator     string       `json:"estimatorNote"`
	Commands      []benchRow   `json:"commands"`
	Totals        benchRow     `json:"totals"`
}

type benchMachine struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Version string `json:"sctxVersion"`
}

// captureStore is a stats.Store that keeps only the most recently recorded
// run, in memory, for the duration of one bench command. It is never a real
// store: bench must never write to the developer's ~/.config/sctx/stats.db,
// let alone the network — reusing run.Service's own accounting is how it
// learns the Tier and token counts a real wrapped run would have recorded,
// without persisting or sending anything.
type captureStore struct {
	last stats.Run
}

func (c *captureStore) Record(_ context.Context, r stats.Run) error {
	c.last = r
	return nil
}
func (c *captureStore) Aggregate(context.Context, stats.AggregateOptions) (stats.Report, error) {
	return stats.Report{}, nil
}
func (c *captureStore) Failures(context.Context, stats.AggregateOptions, int) ([]stats.FailedRun, error) {
	return nil, nil
}
func (c *captureStore) ByClient(context.Context, stats.AggregateOptions) ([]stats.ClientTotals, error) {
	return nil, nil
}
func (c *captureStore) RepeatedRunsToday(context.Context, int) ([]stats.RepeatedRun, error) {
	return nil, nil
}
func (c *captureStore) LatestRawBytes(context.Context, string, string) (int64, bool, error) {
	return 0, false, nil
}
func (c *captureStore) IdenticalRunCount(context.Context, string, string, int64) (int64, error) {
	return 0, nil
}
func (c *captureStore) Close() error { return nil }

// runBench implements `sctx bench`: a reproducible, publishable benchmark
// run entirely against the CURRENT repository, entirely locally.
//
// Every command in the fixed set for the detected language runs TWICE:
// once through a bare runner with no formatting at all (the "raw" pass, what
// an agent would see unwrapped) and once through sctx's own pipeline
// in-process — the same run.Service, registry and tier chain `sctx <cmd>`
// itself uses, never a subprocess of the sctx binary. Nothing bench measures
// is sent anywhere: no telemetry emitter is constructed, and the stats store
// it hands the pipeline is an in-memory capture that is thrown away when the
// command returns.
func runBench(ctx context.Context, cfg config.Config, args []string) int {
	name := ""
	format := "text"
	verbose := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--name":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "sctx: bench: --name requires a value")
				fmt.Fprintln(os.Stderr, benchUsage)
				return 2
			}
			name = args[i]
		case "--format":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "sctx: bench: --format requires a value: text or json")
				fmt.Fprintln(os.Stderr, benchUsage)
				return 2
			}
			switch args[i] {
			case "text", "json":
				format = args[i]
			default:
				fmt.Fprintf(os.Stderr, "sctx: bench: --format: unsupported value %q (want text or json)\n", args[i])
				return 2
			}
		case "--verbose":
			verbose = true
		default:
			fmt.Fprintf(os.Stderr, "sctx: bench: unknown flag %q\n", a)
			fmt.Fprintln(os.Stderr, benchUsage)
			return 2
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: bench: %v\n", err)
		return 1
	}
	language, cmds := benchCommandsFor(cwd)
	registry := buildRegistry()

	rows := make([]benchRow, 0, len(cmds))
	var totalRaw, totalRendered int64
	for _, argv := range cmds {
		row, err := benchOne(ctx, registry, cfg, argv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sctx: bench: running %q: %v\n", run.CommandKey(argv), err)
			continue
		}
		if verbose {
			row.Argv = argv
		}
		rows = append(rows, row)
		totalRaw += row.RawTokens
		totalRendered += row.RenderTokens
	}

	totals := benchRow{RawTokens: totalRaw, RenderTokens: totalRendered}
	if totalRaw > 0 {
		totals.SavedPct = float64(totalRaw-totalRendered) / float64(totalRaw) * 100
	}

	result := benchResult{
		SchemaVersion: benchSchemaVersion,
		Repository:    name,
		Language:      language,
		Machine:       benchMachine{OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version},
		Estimator:     tokenizer.EstimatorNote,
		Commands:      rows,
		Totals:        totals,
	}

	if format == "json" {
		enc := jsontext.NewEncoder(os.Stdout, jsontext.WithIndent("  "))
		if err := json.MarshalEncode(enc, result); err != nil {
			fmt.Fprintf(os.Stderr, "sctx: bench: %v\n", err)
			return 1
		}
		fmt.Println()
		return 0
	}
	renderBenchText(os.Stdout, result, verbose)
	return 0
}

// benchOne runs one command twice — raw, then through the in-process
// pipeline — and returns its row. A non-zero exit from the BENCHMARKED
// command is recorded (ExitCode), never fatal to the run.
func benchOne(ctx context.Context, registry *run.Registry, cfg config.Config, argv []string) (benchRow, error) {
	spill := cfg.MaxOutputBytes
	if spill <= 0 {
		spill = 8 << 20
	}

	// Raw pass: no formatting at all, the same runner sctx itself uses to
	// execute the child, so timing/capture semantics match — just nothing
	// downstream reads the bytes for anything but their length.
	rawRunner := osproc.NewRunner(spill)
	rawOutcome, err := rawRunner.Run(ctx, domexec.Command{Argv: argv})
	if err != nil {
		return benchRow{}, err
	}
	rawBytes := rawOutcome.Stdout.Len() + rawOutcome.Stderr.Len()
	rawOutcome.Stdout.Close()
	rawOutcome.Stderr.Close()

	// Rendered pass: sctx's own in-process pipeline. No telemetry emitter is
	// constructed at all, and the stats store is the in-memory capture above
	// — nothing this pass does can leave the machine or touch a real spool.
	capture := &captureStore{}
	svc := run.NewService(registry, osproc.NewRunner(spill), capture, nil, generic.New(),
		io.Discard, io.Discard,
		run.Options{Version: version, LosslessFallback: jsoncompact.New()})
	exitCode, err := svc.Execute(ctx, argv)
	if err != nil {
		return benchRow{}, err
	}

	row := benchRow{
		Command:      run.CommandKey(argv),
		RawTokens:    tokenizer.Estimate(int64(rawBytes)),
		RenderTokens: capture.last.OutTokens,
		Tier:         capture.last.Tier,
		FormatterKey: capture.last.Formatter,
		ExitCode:     exitCode,
	}
	if row.RawTokens > 0 {
		row.SavedPct = float64(row.RawTokens-row.RenderTokens) / float64(row.RawTokens) * 100
	}
	return row, nil
}

func renderBenchText(w io.Writer, result benchResult, verbose bool) {
	repo := result.Repository
	if repo == "" {
		repo = "(unnamed)"
	}
	fmt.Fprintln(w, "sctx bench")
	fmt.Fprintf(w, "repository:  %s\n", repo)
	fmt.Fprintf(w, "language:    %s\n", result.Language)
	fmt.Fprintf(w, "machine:     %s/%s, sctx %s\n", result.Machine.OS, result.Machine.Arch, result.Machine.Version)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-28s %10s %10s %8s %-10s %s\n", "Command", "Raw", "Rendered", "Saved", "Tier", "Exit")
	for _, r := range result.Commands {
		label := r.Command
		if verbose && len(r.Argv) > 0 {
			label = fmt.Sprint(r.Argv)
		}
		fmt.Fprintf(w, "%-28s %10s %10s %7.1f%% %-10s %d\n",
			truncateBench(label, 28), humanBenchTokens(r.RawTokens), humanBenchTokens(r.RenderTokens), r.SavedPct, r.Tier, r.ExitCode)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-28s %10s %10s %7.1f%%\n", "TOTAL",
		humanBenchTokens(result.Totals.RawTokens), humanBenchTokens(result.Totals.RenderTokens), result.Totals.SavedPct)
	fmt.Fprintln(w)
	fmt.Fprintln(w, result.Estimator)
}

func humanBenchTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncateBench(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
