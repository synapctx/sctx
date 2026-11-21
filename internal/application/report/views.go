package report

import (
	"context"
	"encoding/csv"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/stats"
)

// Period names for Options.Period (`sctx gain --daily|--weekly|--monthly`).
const (
	PeriodDaily   = "daily"
	PeriodWeekly  = "weekly"
	PeriodMonthly = "monthly"
)

const (
	defaultGraphDays   = 30
	maxGraphDays       = 90
	defaultHistoryRows = 20
	defaultTermWidth   = 80

	periodColWidth = 10
	graphLabelCols = 5 // "MM-DD"
	graphValueCols = 7 // widest humanTokens value, e.g. "1000.0K"
	minGraphBar    = 10
	maxGraphBar    = 60
	histCmdWidth   = 24
	histTimeWidth  = 16 // "2006-01-02 15:04"
)

// clock is time.Now, indirected so tests can pin "today" for the graph window.
var clock = time.Now

// periodRow is one line of a --daily/--weekly/--monthly table, and the stable
// per-row JSON shape.
type periodRow struct {
	Period      string  `json:"period"`
	Commands    int64   `json:"commands"`
	RawTokens   int64   `json:"raw_tokens"`
	SavedTokens int64   `json:"saved_tokens"`
	SavedPct    float64 `json:"saved_pct"`
}

func savedPct(saved, raw int64) float64 {
	if raw <= 0 {
		return 0
	}
	return float64(saved) / float64(raw) * 100
}

// periodKey buckets t, in its own (local) zone, into the label for period.
// Weeks are ISO weeks, labelled with the Monday that starts them.
func periodKey(t time.Time, period string) string {
	switch period {
	case PeriodMonthly:
		return t.Format("2006-01")
	case PeriodWeekly:
		wd := (int(t.Weekday()) + 6) % 7 // Monday = 0
		monday := time.Date(t.Year(), t.Month(), t.Day()-wd, 0, 0, 0, 0, t.Location())
		return monday.Format("2006-01-02")
	default:
		return t.Format("2006-01-02")
	}
}

// bucket sums points into period rows, newest first.
func bucket(points []stats.TimePoint, period string) []periodRow {
	idx := map[string]*periodRow{}
	for _, p := range points {
		k := periodKey(p.At.Local(), period)
		r := idx[k]
		if r == nil {
			r = &periodRow{Period: k}
			idx[k] = r
		}
		r.Commands++
		r.RawTokens += p.RawTokens
		r.SavedTokens += p.SavedTokens
	}
	rows := make([]periodRow, 0, len(idx))
	for _, r := range idx {
		r.SavedPct = savedPct(r.SavedTokens, r.RawTokens)
		rows = append(rows, *r)
	}
	// Labels are zero-padded ISO dates, so a string sort is a time sort.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Period > rows[j].Period })
	return rows
}

// periodLabel is the table's first-column header.
func periodLabel(period string) string {
	switch period {
	case PeriodMonthly:
		return "Month"
	case PeriodWeekly:
		return "Week of"
	default:
		return "Day"
	}
}

func renderPeriods(ctx context.Context, store stats.Store, w io.Writer, opts Options) error {
	points, err := store.Timeline(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: opts.Since})
	if err != nil {
		return fmt.Errorf("aggregating %s stats: %w", opts.Period, err)
	}
	rows := bucket(points, opts.Period)
	switch opts.Format {
	case "json":
		return renderPeriodsJSON(w, rows, opts)
	case "csv":
		return writePeriodCSV(w, rows)
	case "markdown":
		return renderPeriodsMarkdown(w, rows, opts)
	}
	return renderPeriodsText(w, rows, opts)
}

type jsonPeriods struct {
	Scope      string      `json:"scope"` // "global" | "project"
	Repository string      `json:"repository,omitempty"`
	Since      *time.Time  `json:"since,omitzero"`
	Period     string      `json:"period"`
	Rows       []periodRow `json:"rows"`
}

func renderPeriodsJSON(w io.Writer, rows []periodRow, opts Options) error {
	if rows == nil {
		rows = []periodRow{}
	}
	out := jsonPeriods{Scope: "global", Period: opts.Period, Rows: rows}
	if opts.Repository != "" {
		out.Scope = "project"
		out.Repository = opts.Repository
	}
	if !opts.Since.IsZero() {
		since := opts.Since
		out.Since = &since
	}
	enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
	return json.MarshalEncode(enc, out)
}

func writePeriodCSV(w io.Writer, rows []periodRow) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"period", "commands", "raw_tokens", "saved_tokens", "saved_pct"})
	for _, r := range rows {
		_ = cw.Write([]string{r.Period, strconv.FormatInt(r.Commands, 10), strconv.FormatInt(r.RawTokens, 10),
			strconv.FormatInt(r.SavedTokens, 10), fmt.Sprintf("%.1f", r.SavedPct)})
	}
	cw.Flush()
	return cw.Error()
}

func renderPeriodsMarkdown(w io.Writer, rows []periodRow, opts Options) error {
	fmt.Fprintf(w, "| %s | Commands | Raw tokens | Saved tokens | Saved %% |\n", periodLabel(opts.Period))
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: |")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %d | %s | %s | %s |\n",
			r.Period, r.Commands, humanTokens(r.RawTokens), humanTokens(r.SavedTokens), pctString(r.SavedPct))
	}
	return nil
}

// scopeLines prints the same Project Scope/Window lines the summary does, only
// when the report is actually scoped.
func scopeLines(w io.Writer, p palette, opts Options) {
	if opts.Repository != "" {
		fmt.Fprintf(w, "%s %s\n", p.dim(hdr("Project Scope:")), p.cyan(opts.Repository))
	}
	if !opts.Since.IsZero() {
		fmt.Fprintf(w, "%s since %s\n", p.dim(hdr("Window:")), opts.Since.Format(time.RFC3339))
	}
}

func renderPeriodsText(w io.Writer, rows []periodRow, opts Options) error {
	p := palette{on: opts.Color}
	const width = periodColWidth + 1 + 9 + 1 + 9 + 1 + 9 + 1 + 8
	title := strings.ToUpper(opts.Period[:1]) + opts.Period[1:]
	fmt.Fprintln(w, p.boldGreen("sctx Token Savings — "+title))
	fmt.Fprintln(w, p.rule("═", width))
	scopeLines(w, p, opts)
	if len(rows) == 0 {
		fmt.Fprintln(w, p.dim("No runs recorded."))
		return nil
	}
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%-*s %9s %9s %9s %8s",
		periodColWidth, periodLabel(opts.Period), "Commands", "Raw", "Saved", "Saved%")))
	fmt.Fprintln(w, p.rule("─", width))
	for _, r := range rows {
		fmt.Fprintf(w, "%s %9d %9s %s %s\n",
			p.cyan(fmt.Sprintf("%-*s", periodColWidth, r.Period)), r.Commands, humanTokens(r.RawTokens),
			p.green(fmt.Sprintf("%9s", humanTokens(r.SavedTokens))),
			p.pct(fmt.Sprintf("%8s", pctString(r.SavedPct)), r.SavedPct))
	}
	return nil
}

// graphDay is one bar of the --graph chart (and one row of its non-text forms).
type graphDay struct {
	Day         string `json:"day"`
	SavedTokens int64  `json:"saved_tokens"`
}

// graphDays resolves how many days the chart covers: the --since window when
// given, else 30, never more than maxGraphDays so a line stays one screen row.
func graphDays(since, now time.Time) int {
	if since.IsZero() {
		return defaultGraphDays
	}
	d := int(now.Sub(since).Hours()/24) + 1
	if d < 1 {
		d = 1
	}
	if d > maxGraphDays {
		d = maxGraphDays
	}
	return d
}

func renderGraph(ctx context.Context, store stats.Store, w io.Writer, opts Options) error {
	now := clock().Local()
	days := graphDays(opts.Since, now)
	start := time.Date(now.Year(), now.Month(), now.Day()-(days-1), 0, 0, 0, 0, now.Location())
	points, err := store.Timeline(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: start})
	if err != nil {
		return fmt.Errorf("aggregating graph stats: %w", err)
	}
	saved := map[string]int64{}
	for _, pt := range points {
		saved[pt.At.Local().Format("2006-01-02")] += pt.SavedTokens
	}
	series := make([]graphDay, 0, days) // oldest first, zero days included
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		series = append(series, graphDay{Day: d, SavedTokens: saved[d]})
	}

	switch opts.Format {
	case "json":
		out := struct {
			Scope      string     `json:"scope"`
			Repository string     `json:"repository,omitempty"`
			Days       []graphDay `json:"days"`
		}{Scope: "global", Days: series}
		if opts.Repository != "" {
			out.Scope = "project"
			out.Repository = opts.Repository
		}
		enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
		return json.MarshalEncode(enc, out)
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"day", "saved_tokens"})
		for _, d := range series {
			_ = cw.Write([]string{d.Day, strconv.FormatInt(d.SavedTokens, 10)})
		}
		cw.Flush()
		return cw.Error()
	case "markdown":
		fmt.Fprintln(w, "```")
		writeGraph(w, palette{}, series, opts.Width)
		fmt.Fprintln(w, "```")
		return nil
	}
	p := palette{on: opts.Color}
	fmt.Fprintln(w, p.boldGreen(fmt.Sprintf("sctx Tokens Saved per Day — last %d days", days)))
	scopeLines(w, p, opts)
	writeGraph(w, p, series, opts.Width)
	return nil
}

// writeGraph draws one `MM-DD ████░░░░ 12.3K` line per day, oldest first, in
// the same █/░ style as the summary meters. width is the terminal width; <=0
// means unknown and falls back to 80 columns.
func writeGraph(w io.Writer, p palette, series []graphDay, width int) {
	if width <= 0 {
		width = defaultTermWidth
	}
	barW := width - graphLabelCols - 1 - 1 - graphValueCols
	barW = min(max(barW, minGraphBar), maxGraphBar)
	var maxSaved int64
	for _, d := range series {
		maxSaved = max(maxSaved, d.SavedTokens)
	}
	for _, d := range series {
		filled := 0
		if maxSaved > 0 && d.SavedTokens > 0 {
			filled = max(int(float64(d.SavedTokens)/float64(maxSaved)*float64(barW)+0.5), 1)
		}
		label := d.Day[len("2006-"):]
		fmt.Fprintf(w, "%s %s %s\n", p.dim(label), p.meterBar("█", "░", filled, barW),
			p.green(fmt.Sprintf("%*s", graphValueCols, humanTokens(d.SavedTokens))))
	}
}

// jsonHistoryRun is the stable per-row shape for `--history --format json`.
type jsonHistoryRun struct {
	At          time.Time `json:"at"`
	Command     string    `json:"command"`
	Tier        string    `json:"tier"`
	RawTokens   int64     `json:"raw_tokens"`
	OutTokens   int64     `json:"out_tokens"`
	SavedPct    float64   `json:"saved_pct"`
	SavedTokens int64     `json:"saved_tokens"`
}

// renderHistory lists the last N wrapped commands. The command column is the
// normalized program key (see CommandKey), never Argv: `--share` is built from
// the same field precisely because argv carries paths and secrets, and this
// view is just as likely to be pasted into an issue.
func renderHistory(ctx context.Context, store stats.Store, w io.Writer, opts Options) error {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultHistoryRows
	}
	runs, err := store.Recent(ctx, stats.AggregateOptions{Repository: opts.Repository, Since: opts.Since}, limit)
	if err != nil {
		return fmt.Errorf("querying history: %w", err)
	}
	rows := make([]jsonHistoryRun, 0, len(runs))
	for _, r := range runs {
		rows = append(rows, jsonHistoryRun{At: r.At, Command: r.Command, Tier: r.Tier, RawTokens: r.RawTokens,
			OutTokens: r.OutTokens, SavedTokens: r.SavedTokens, SavedPct: savedPct(r.SavedTokens, r.RawTokens)})
	}

	switch opts.Format {
	case "json":
		out := struct {
			Scope      string           `json:"scope"`
			Repository string           `json:"repository,omitempty"`
			History    []jsonHistoryRun `json:"history"`
		}{Scope: "global", History: rows}
		if opts.Repository != "" {
			out.Scope = "project"
			out.Repository = opts.Repository
		}
		enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
		return json.MarshalEncode(enc, out)
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"at", "command", "tier", "raw_tokens", "out_tokens", "saved_tokens", "saved_pct"})
		for _, r := range rows {
			_ = cw.Write([]string{r.At.Format(time.RFC3339), r.Command, r.Tier, strconv.FormatInt(r.RawTokens, 10),
				strconv.FormatInt(r.OutTokens, 10), strconv.FormatInt(r.SavedTokens, 10), fmt.Sprintf("%.1f", r.SavedPct)})
		}
		cw.Flush()
		return cw.Error()
	case "markdown":
		fmt.Fprintln(w, "| Time | Command | Tier | Raw → Out | Saved % |")
		fmt.Fprintln(w, "| --- | --- | --- | ---: | ---: |")
		for _, r := range rows {
			fmt.Fprintf(w, "| %s | `%s` | %s | %s → %s | %s |\n", r.At.Local().Format("2006-01-02 15:04"),
				r.Command, r.Tier, humanTokens(r.RawTokens), humanTokens(r.OutTokens), pctString(r.SavedPct))
		}
		return nil
	}

	p := palette{on: opts.Color}
	const width = histTimeWidth + 1 + histCmdWidth + 1 + 10 + 1 + 15 + 1 + 7
	fmt.Fprintln(w, p.boldGreen("sctx Recent Commands"))
	fmt.Fprintln(w, p.rule("═", width))
	scopeLines(w, p, opts)
	if len(rows) == 0 {
		fmt.Fprintln(w, p.dim("No runs recorded."))
		return nil
	}
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%-*s %-*s %-10s %15s %7s",
		histTimeWidth, "Time", histCmdWidth, "Command", "Tier", "Raw → Out", "Saved%")))
	fmt.Fprintln(w, p.rule("─", width))
	for _, r := range rows {
		tm := fmt.Sprintf("%-*s", histTimeWidth, r.At.Local().Format("2006-01-02 15:04"))
		cmd := fmt.Sprintf("%-*s", histCmdWidth, truncate(r.Command, histCmdWidth))
		tier := fmt.Sprintf("%-10s", r.Tier)
		tokens := fmt.Sprintf("%15s", humanTokens(r.RawTokens)+" → "+humanTokens(r.OutTokens))
		pct := fmt.Sprintf("%7s", pctString(r.SavedPct))
		fmt.Fprintf(w, "%s %s %s %s %s\n", p.dim(tm), p.cyan(cmd), p.tier(r.Tier, tier), tokens, p.pct(pct, r.SavedPct))
	}
	return nil
}

// writeSummaryCSV renders the default summary as CSV: one "(total)" row then
// one row per command, using the program key (never argv).
func writeSummaryCSV(w io.Writer, rep stats.Report) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"command", "runs", "raw_tokens", "out_tokens", "saved_tokens", "saved_pct", "avg_ms"})
	row := func(name string, t stats.Totals) {
		_ = cw.Write([]string{name, strconv.FormatInt(t.Runs, 10), strconv.FormatInt(t.RawTokens, 10),
			strconv.FormatInt(t.OutTokens, 10), strconv.FormatInt(t.SavedTokens, 10),
			fmt.Sprintf("%.1f", savedPct(t.SavedTokens, t.RawTokens)), strconv.FormatInt(t.AvgMS, 10)})
	}
	row("(total)", rep.Global)
	for _, c := range rep.ByCommand {
		row(c.Command, c.Totals)
	}
	cw.Flush()
	return cw.Error()
}
