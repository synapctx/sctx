// Package report renders the `sctx gain` token-savings report from the
// local stats store.
package report

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/stats"
)

const (
	maxCommandRows  = 10
	maxFailureRows  = 20
	commandColWidth = 26
	meterWidth      = 24
	impactWidth     = 10
	failCmdWidth    = 40
	failAnomWidth   = 16
	rfc3339Width    = 20 // "2006-01-02T15:04:05Z"

	// tableWidth is the exact printed width of a `By Command` row (and its
	// header), derived from the column layout in renderReportText so the
	// horizontal rules span the whole table, impact bar included. Layout:
	// "%3s  %-*s %6s %8s %7s %8s  %s" → rank(3) + 2 + name + 1 + count(6) +
	// 1 + saved(8) + 1 + avg(7) + 1 + time(8) + 2 + impact.
	tableWidth = 3 + 2 + commandColWidth + 1 + 6 + 1 + 8 + 1 + 7 + 1 + 8 + 2 + impactWidth

	// failTableWidth is the printed width of a degradation-log row, from
	// "%-*s %-10s %-*s %s" → cmd + 1 + tier(10) + 1 + anomaly + 1 + at.
	failTableWidth = failCmdWidth + 1 + 10 + 1 + failAnomWidth + 1 + rfc3339Width
)

// Options configures Render's scope, mode, and output format.
type Options struct {
	// Repository, when non-empty, scopes the report to this "org/repo"
	// (`sctx gain --project`/`-p`).
	Repository string
	// Since, when non-zero, scopes the report to runs at/after this instant
	// (`sctx gain --since <dur>`).
	Since time.Time
	// Failures renders the degradation log (runs sctx couldn't compress or
	// that hit a render anomaly) instead of the savings table
	// (`sctx gain --failures`/`-F`).
	Failures bool
	// Format selects the output encoding: "" or "text" (default) renders
	// the human-readable report; "json" emits it as JSON
	// (`sctx gain --format json`).
	Format string
	// Limit caps the number of rows in the failures log. <=0 defaults to
	// maxFailureRows. Unused outside Failures mode.
	Limit int
	// Color enables ANSI-colored output for the text renderers. The caller
	// sets it (TTY stdout, NO_COLOR unset); JSON output ignores it.
	Color bool
	// ByClient renders a breakdown by which coding agent ran each command
	// (`sctx gain --by-client`), in addition to the normal report.
	ByClient bool
}

// Render writes the `sctx gain` report to w per opts.
func Render(ctx context.Context, store stats.Store, w io.Writer, opts Options) error {
	if opts.Failures {
		return renderFailures(ctx, store, w, opts)
	}

	rep, err := store.Aggregate(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: opts.Since})
	if err != nil {
		return fmt.Errorf("aggregating stats: %w", err)
	}

	var byClient []stats.ClientTotals
	if opts.ByClient {
		byClient, err = store.ByClient(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: opts.Since})
		if err != nil {
			return fmt.Errorf("aggregating by client: %w", err)
		}
	}
	// Computed unconditionally (text mode only, below): a local read of the
	// local store, telling the developer something useful about their own
	// habits — not gated behind --by-client, which is a different axis
	// (WHO ran commands, not WHICH commands repeat).
	var repeated []stats.RepeatedRun
	if opts.Format != "json" {
		repeated, err = store.RepeatedRunsToday(ctx, 0)
		if err != nil {
			return fmt.Errorf("aggregating repeated runs: %w", err)
		}
	}

	if opts.Format == "json" {
		return renderReportJSON(w, rep, byClient, opts)
	}
	return renderReportText(w, rep, byClient, repeated, opts)
}

func renderReportText(w io.Writer, rep stats.Report, byClient []stats.ClientTotals, repeated []stats.RepeatedRun, opts Options) error {
	p := palette{on: opts.Color}
	g := rep.Global
	pct := 0.0
	if g.RawTokens > 0 {
		pct = float64(g.SavedTokens) / float64(g.RawTokens) * 100
	}

	fmt.Fprintln(w, p.boldGreen("sctx Token Savings"))
	fmt.Fprintln(w, p.rule("═", tableWidth))
	// Scope lines are only printed when the report is actually scoped, so
	// the default (no --project/--since) output is unchanged.
	if opts.Repository != "" {
		fmt.Fprintf(w, "%s %s\n", p.dim("Project Scope:  "), p.cyan(opts.Repository))
	}
	if !opts.Since.IsZero() {
		fmt.Fprintf(w, "%s since %s\n", p.dim("Window:         "), opts.Since.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "%s %s\n", p.dim("Total commands: "), p.bold(fmt.Sprintf("%d", g.Runs)))
	fmt.Fprintf(w, "%s %s\n", p.dim("Raw tokens:     "), humanTokens(g.RawTokens))
	fmt.Fprintf(w, "%s %s\n", p.dim("Output tokens:  "), humanTokens(g.OutTokens))
	fmt.Fprintf(w, "%s %s (%s)\n", p.dim("Tokens saved:   "),
		p.boldGreen(humanTokens(g.SavedTokens)), p.pct(pctString(pct), pct))
	fmt.Fprintf(w, "%s %s (avg %s)\n", p.dim("Total exec time:"),
		(time.Duration(rep.TotalExecMS) * time.Millisecond).Round(time.Second),
		(time.Duration(g.AvgMS) * time.Millisecond).Round(time.Millisecond))
	effFilled := int(pct / 100 * meterWidth)
	fmt.Fprintf(w, "%s %s %s\n", p.dim("Efficiency:     "),
		p.meterBar("█", "░", effFilled, meterWidth), p.bold(p.pct(pctString(pct), pct)))

	if len(rep.ByCommand) > 0 {
		renderByCommand(w, p, rep, opts)
	}

	if len(byClient) > 0 {
		renderByClient(w, p, byClient)
	}

	if len(repeated) > 0 {
		renderRepeatedRunsToday(w, p, repeated)
	}

	return nil
}

func renderByCommand(w io.Writer, p palette, rep stats.Report, opts Options) {
	g := rep.Global
	fmt.Fprintln(w)
	fmt.Fprintln(w, p.boldCyan("By Command"))
	fmt.Fprintln(w, p.rule("─", tableWidth))
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%3s  %-*s %6s %8s %7s %8s  %s",
		"#", commandColWidth, "Command", "Count", "Saved", "Avg%", "Time", "Impact")))
	fmt.Fprintln(w, p.rule("─", tableWidth))

	top := rep.ByCommand
	if len(top) > maxCommandRows {
		top = top[:maxCommandRows]
	}
	for i, c := range top {
		cmdPct := 0.0
		if c.RawTokens > 0 {
			cmdPct = float64(c.SavedTokens) / float64(c.RawTokens) * 100
		}
		impact := 0.0
		if g.SavedTokens > 0 {
			impact = float64(c.SavedTokens) / float64(g.SavedTokens)
		}
		// Pad each cell to its column width as plain text first, then color
		// it — ANSI escape bytes would otherwise throw off %-*s alignment.
		num := fmt.Sprintf("%2d.", i+1)
		name := fmt.Sprintf("%-*s", commandColWidth, truncate(c.Command, commandColWidth))
		count := fmt.Sprintf("%6d", c.Runs)
		saved := fmt.Sprintf("%8s", humanTokens(c.SavedTokens))
		avg := fmt.Sprintf("%7s", pctString(cmdPct))
		tm := fmt.Sprintf("%8s", (time.Duration(c.AvgMS) * time.Millisecond).Round(time.Millisecond))
		impactFilled := int(impact * impactWidth)
		fmt.Fprintf(w, "%s  %s %s %s %s %s  %s\n",
			p.dim(num), p.cyan(name), count, p.green(saved),
			p.pct(avg, cmdPct), p.dim(tm), p.meterBar("█", "░", impactFilled, impactWidth))
	}
}

// renderByClient prints the `sctx gain --by-client` breakdown: the same
// shape as By Command, grouped by which coding agent ran each command
// instead of which command ran.
func renderByClient(w io.Writer, p palette, byClient []stats.ClientTotals) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, p.boldCyan("By Client"))
	fmt.Fprintln(w, p.rule("─", tableWidth))
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%-*s %6s %8s %7s %8s",
		commandColWidth, "Client", "Count", "Saved", "Avg%", "Time")))
	fmt.Fprintln(w, p.rule("─", tableWidth))
	for _, c := range byClient {
		label := c.Client
		if label == "" {
			label = "(unknown)"
		}
		cmdPct := 0.0
		if c.RawTokens > 0 {
			cmdPct = float64(c.SavedTokens) / float64(c.RawTokens) * 100
		}
		name := fmt.Sprintf("%-*s", commandColWidth, truncate(label, commandColWidth))
		count := fmt.Sprintf("%6d", c.Runs)
		saved := fmt.Sprintf("%8s", humanTokens(c.SavedTokens))
		avg := fmt.Sprintf("%7s", pctString(cmdPct))
		tm := fmt.Sprintf("%8s", (time.Duration(c.AvgMS) * time.Millisecond).Round(time.Millisecond))
		fmt.Fprintf(w, "%s %s %s %s %s\n", p.cyan(name), count, p.green(saved), p.pct(avg, cmdPct), p.dim(tm))
	}
}

// renderRepeatedRunsToday prints the "you ran this again" line: a local read
// of the local store, not anything that could leave the machine.
func renderRepeatedRunsToday(w io.Writer, p palette, repeated []stats.RepeatedRun) {
	total := int64(0)
	parts := make([]string, 0, len(repeated))
	for _, rr := range repeated {
		total += rr.Count
		parts = append(parts, fmt.Sprintf("%s %d", rr.Argv, rr.Count))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s %s (top: %s)\n",
		p.dim("Repeated identical runs today:"), p.bold(fmt.Sprintf("%d", total)), strings.Join(parts, ", "))
}

// jsonReport is the stable `sctx gain --format json` shape.
type jsonReport struct {
	Scope       string                `json:"scope"` // "global" | "project"
	Repository  string                `json:"repository,omitempty"`
	Since       *time.Time            `json:"since,omitzero"`        // --since cutoff, if scoped
	EarliestRun *time.Time            `json:"earliest_run,omitzero"` // earliest run in the (scoped) data
	Global      stats.Totals          `json:"global"`
	ByCommand   []stats.CommandTotals `json:"by_command"`
	ByClient    []stats.ClientTotals  `json:"by_client,omitempty"`
	TotalExecMS int64                 `json:"total_exec_ms"`
}

func renderReportJSON(w io.Writer, rep stats.Report, byClient []stats.ClientTotals, opts Options) error {
	out := jsonReport{
		Scope:       "global",
		Global:      rep.Global,
		ByCommand:   rep.ByCommand,
		ByClient:    byClient,
		TotalExecMS: rep.TotalExecMS,
	}
	if opts.Repository != "" {
		out.Scope = "project"
		out.Repository = opts.Repository
	}
	if !opts.Since.IsZero() {
		since := opts.Since
		out.Since = &since
	}
	if !rep.Since.IsZero() {
		earliest := rep.Since
		out.EarliestRun = &earliest
	}
	enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
	return json.MarshalEncode(enc, out)
}

// renderFailures renders the degradation log: runs sctx couldn't compress
// (tier fell to verbatim) or that hit a render anomaly. This is the local
// self-diagnosis of compression leaks (`sctx gain --failures`/`-F`).
func renderFailures(ctx context.Context, store stats.Store, w io.Writer, opts Options) error {
	limit := opts.Limit
	if limit <= 0 {
		limit = maxFailureRows
	}
	rows, err := store.Failures(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: opts.Since}, limit)
	if err != nil {
		return fmt.Errorf("querying failures: %w", err)
	}
	if opts.Format == "json" {
		return renderFailuresJSON(w, rows, opts)
	}
	return renderFailuresText(w, rows, opts)
}

func renderFailuresText(w io.Writer, rows []stats.FailedRun, opts Options) error {
	p := palette{on: opts.Color}
	fmt.Fprintln(w, p.wrap("sctx Degradation Log", ansiBold, ansiYellow))
	fmt.Fprintln(w, p.rule("═", failTableWidth))
	if opts.Repository != "" {
		fmt.Fprintf(w, "%s %s\n", p.dim("Project Scope:  "), p.cyan(opts.Repository))
	}
	if !opts.Since.IsZero() {
		fmt.Fprintf(w, "%s since %s\n", p.dim("Window:         "), opts.Since.Format(time.RFC3339))
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, p.dim("No degraded runs recorded."))
		return nil
	}

	// Deliberate declines are separated from genuine degradations. This report is the
	// coverage meter that decides what to build next, and listing `go test -list` —
	// which bypasses its formatter ON PURPOSE so its answer survives — as something to
	// investigate sent exactly that signal.
	var faults, noImprovement, benign int
	for _, r := range rows {
		switch classifyAnomaly(r.Anomaly) {
		case anomalyFault:
			faults++
		case anomalyNoImprovement:
			noImprovement++
		default:
			benign++
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%-*s %-10s %-*s %s",
		failCmdWidth, "Command", "Tier", failAnomWidth, "Anomaly", "At")))
	fmt.Fprintln(w, p.rule("─", failTableWidth))
	for _, r := range rows {
		cmd := r.Argv
		if cmd == "" {
			cmd = r.Command
		}
		anomaly := r.Anomaly
		switch classifyAnomaly(anomaly) {
		case anomalyDeclined:
			anomaly = "by design (formatter declined)"
		case anomalyNoGain:
			anomaly = "no gain"
		case anomalyNoImprovement:
			anomaly = "no improvement (guard held)"
		}
		// Pad plain first, color after, so ANSI bytes don't skew alignment.
		cmdCell := fmt.Sprintf("%-*s", failCmdWidth, truncate(cmd, failCmdWidth))
		tierCell := fmt.Sprintf("%-10s", r.Tier)
		anomCell := fmt.Sprintf("%-*s", failAnomWidth, truncate(anomaly, failAnomWidth))
		fmt.Fprintf(w, "%s %s %s %s\n",
			cmdCell, p.tier(r.Tier, tierCell), p.wrapAnomaly(anomaly, anomCell), p.dim(r.At.Format(time.RFC3339)))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, p.dim(fmt.Sprintf(
		"%d fault(s) worth investigating · %d no improvement (guard held) · %d by design or no gain.",
		faults, noImprovement, benign)))
	return nil
}

// anomalyClass separates the reasons a run ended at verbatim. Only one of them is a
// fault, and conflating them is what made this report point the roadmap at working
// behaviour.
type anomalyClass int

const (
	// anomalyNoGain: verbatim with nothing recorded — there was no gain to be had.
	anomalyNoGain anomalyClass = iota
	// anomalyDeclined: a formatter deliberately bypassed itself, e.g. `go test -list`
	// so the command's own answer survives.
	anomalyDeclined
	// anomalyNoImprovement: a tier produced valid output that was not smaller, and the
	// guard rejected it. The safety net working exactly as designed — interesting in
	// AGGREGATE (a formatter that never improves has a real gap) but not an incident.
	// Measured 2026-07-29: grep saves 55% across 636 runs, so its handful of these are
	// small inputs where compression cannot help.
	anomalyNoImprovement
	// anomalyFault: an empty render for non-empty output, a formatter error, or a
	// panic. The only class worth investigating one row at a time.
	anomalyFault
)

func classifyAnomaly(anomaly string) anomalyClass {
	switch {
	case anomaly == "":
		return anomalyNoGain
	case strings.HasPrefix(anomaly, "declined:"):
		return anomalyDeclined
	case strings.Contains(anomaly, "not smaller than raw output"):
		return anomalyNoImprovement
	default:
		return anomalyFault
	}
}

// jsonFailedRun is the stable per-row shape for `sctx gain --failures
// --format json`.
type jsonFailedRun struct {
	Command string    `json:"command"` // full argv when available, else the normalized command key
	Tier    string    `json:"tier"`
	Anomaly string    `json:"anomaly,omitempty"`
	At      time.Time `json:"at"`
}

type jsonFailures struct {
	Scope      string          `json:"scope"` // "global" | "project"
	Repository string          `json:"repository,omitempty"`
	Failures   []jsonFailedRun `json:"failures"`
}

func renderFailuresJSON(w io.Writer, rows []stats.FailedRun, opts Options) error {
	out := jsonFailures{Scope: "global", Failures: []jsonFailedRun{}}
	if opts.Repository != "" {
		out.Scope = "project"
		out.Repository = opts.Repository
	}
	for _, r := range rows {
		cmd := r.Argv
		if cmd == "" {
			cmd = r.Command
		}
		out.Failures = append(out.Failures, jsonFailedRun{Command: cmd, Tier: r.Tier, Anomaly: r.Anomaly, At: r.At})
	}
	enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
	return json.MarshalEncode(enc, out)
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
