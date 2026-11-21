package report

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/domain/stats"
)

// viewsNow is the pinned "today" (local noon) for the view tests.
func viewsNow() time.Time {
	return time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local) // a Wednesday
}

func at(daysAgo int, hour int) time.Time {
	n := viewsNow()
	return time.Date(n.Year(), n.Month(), n.Day()-daysAgo, hour, 0, 0, 0, time.Local)
}

// seedViews records runs across days/weeks/months. Argv carries a secret that
// must never appear in any view.
func seedViews(t *testing.T) *sqlite.Store {
	t.Helper()
	store := newTestStore(t)
	mk := func(id string, when time.Time, cmd, tier, repo string, raw, out int64) stats.Run {
		return stats.Run{ID: id, At: when, Command: cmd, Argv: cmd + " --token=SECRET123", Tier: tier,
			RawTokens: raw, OutTokens: out, SavedTokens: raw - out, Repository: repo}
	}
	seed(t, store, []stats.Run{
		mk("1", at(0, 9), "go test", "aggressive", "o/a", 1000, 200),
		mk("2", at(0, 10), "git log", "relaxed", "o/a", 500, 250),
		mk("3", at(1, 9), "go test", "aggressive", "o/b", 2000, 400),
		mk("4", at(8, 9), "make", "verbatim", "o/a", 300, 300),      // previous ISO week (Mon 2026-11-09)
		mk("5", at(40, 9), "go vet", "aggressive", "o/a", 800, 100), // 2026-10
	})
	return store
}

func renderViews(t *testing.T, store stats.Store, opts Options) string {
	t.Helper()
	prev := clock
	clock = viewsNow
	t.Cleanup(func() { clock = prev })
	var buf bytes.Buffer
	if err := Render(context.Background(), store, &buf, opts); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func csvRows(t *testing.T, s string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatalf("invalid csv: %v\n%s", err, s)
	}
	return rows
}

func TestPeriodTables(t *testing.T) {
	store := seedViews(t)
	tests := []struct {
		name   string
		opts   Options
		first  []string // expected first data row (CSV)
		nrows  int
		lastID string // period of last row
	}{
		{"daily", Options{Period: PeriodDaily}, []string{"2026-11-18", "2", "1500", "1050", "70.0"}, 4, "2026-10-09"},
		{"weekly", Options{Period: PeriodWeekly}, []string{"2026-11-16", "3", "3500", "2650", "75.7"}, 3, "2026-10-05"},
		{"monthly", Options{Period: PeriodMonthly}, []string{"2026-11", "4", "3800", "2650", "69.7"}, 2, "2026-10"},
		{"project scoped", Options{Period: PeriodDaily, Repository: "o/b"}, []string{"2026-11-17", "1", "2000", "1600", "80.0"}, 1, "2026-11-17"},
		{"since scoped", Options{Period: PeriodMonthly, Since: at(2, 0)}, []string{"2026-11", "3", "3500", "2650", "75.7"}, 1, "2026-11"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Format = "csv"
			rows := csvRows(t, renderViews(t, store, tc.opts))
			if got := strings.Join(rows[0], ","); got != "period,commands,raw_tokens,saved_tokens,saved_pct" {
				t.Fatalf("header = %s", got)
			}
			data := rows[1:]
			if len(data) != tc.nrows {
				t.Fatalf("got %d rows, want %d: %v", len(data), tc.nrows, data)
			}
			if got, want := strings.Join(data[0], ","), strings.Join(tc.first, ","); got != want {
				t.Errorf("first row = %s, want %s (newest first)", got, want)
			}
			if data[len(data)-1][0] != tc.lastID {
				t.Errorf("last period = %s, want %s", data[len(data)-1][0], tc.lastID)
			}
		})
	}
}

func TestPeriodFormats(t *testing.T) {
	store := seedViews(t)
	tests := []struct {
		format string
		want   []string
	}{
		{"text", []string{"Day", "Commands", "Saved%", "2026-11-18", "70.0%"}},
		{"json", []string{`"period": "daily"`, `"period": "2026-11-18"`, `"saved_pct": 70`}},
		{"markdown", []string{"| Day | Commands |", "| 2026-11-18 | 2 |", "70.0%"}},
	}
	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			out := renderViews(t, store, Options{Period: PeriodDaily, Format: tc.format})
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
		})
	}
}

func TestEmptyStoreViews(t *testing.T) {
	store := newTestStore(t)
	for _, opts := range []Options{
		{Period: PeriodDaily}, {Period: PeriodDaily, Format: "json"}, {Period: PeriodWeekly, Format: "csv"},
		{History: true}, {History: true, Format: "json"}, {Graph: true}, {Format: "csv"},
	} {
		out := renderViews(t, store, opts)
		if opts.Format == "json" && strings.Contains(out, "null") {
			t.Errorf("%+v: json must use [] not null:\n%s", opts, out)
		}
	}
}

func TestGraph(t *testing.T) {
	store := seedViews(t)
	t.Run("default is 30 zero-filled days, oldest first", func(t *testing.T) {
		out := renderViews(t, store, Options{Graph: true})
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != 31 { // title + 30 days
			t.Fatalf("got %d lines:\n%s", len(lines), out)
		}
		if !strings.HasPrefix(lines[1], "10-20 ") || !strings.HasPrefix(lines[30], "11-18 ") {
			t.Errorf("window/order wrong: first=%q last=%q", lines[1], lines[30])
		}
		if !strings.Contains(lines[30], "█") || !strings.Contains(lines[30], "1.1K") {
			t.Errorf("today bar missing: %q", lines[30])
		}
		if !strings.Contains(lines[1], "░") || strings.Contains(lines[1], "█") {
			t.Errorf("empty day should be all ░: %q", lines[1])
		}
	})
	t.Run("width fallback and clamp", func(t *testing.T) {
		tests := []struct {
			width int
			want  int // runes per bar line
		}{
			{0, 5 + 1 + 60 + 1 + 7},   // unknown -> 80 cols, bar capped at 60
			{40, 40},                  // narrow
			{500, 5 + 1 + 60 + 1 + 7}, // capped bar
			{10, 5 + 1 + 10 + 1 + 7},  // floor
		}
		for _, tc := range tests {
			out := renderViews(t, store, Options{Graph: true, Width: tc.width})
			line := strings.Split(out, "\n")[1]
			if n := len([]rune(line)); n != tc.want {
				t.Errorf("width %d: line is %d cols, want %d: %q", tc.width, n, tc.want, line)
			}
		}
	})
	t.Run("since narrows window", func(t *testing.T) {
		out := renderViews(t, store, Options{Graph: true, Since: at(6, 0), Format: "csv"})
		if rows := csvRows(t, out); len(rows) != 1+7 {
			t.Errorf("got %d rows, want header+7:\n%s", len(rows), out)
		}
	})
	t.Run("json and markdown", func(t *testing.T) {
		if out := renderViews(t, store, Options{Graph: true, Format: "json"}); !strings.Contains(out, `"saved_tokens": 1050`) {
			t.Errorf("json: %s", out)
		}
		if out := renderViews(t, store, Options{Graph: true, Format: "markdown"}); !strings.HasPrefix(out, "```\n") {
			t.Errorf("markdown not fenced: %s", out)
		}
	})
}

func TestHistory(t *testing.T) {
	store := seedViews(t)
	t.Run("newest first, limited, never argv", func(t *testing.T) {
		for _, f := range []string{"text", "json", "csv", "markdown"} {
			out := renderViews(t, store, Options{History: true, Limit: 3, Format: f})
			if strings.Contains(out, "SECRET123") || strings.Contains(out, "--token") {
				t.Errorf("%s leaked argv:\n%s", f, out)
			}
		}
		rows := csvRows(t, renderViews(t, store, Options{History: true, Limit: 3, Format: "csv"}))
		if len(rows) != 4 {
			t.Fatalf("want header+3 rows, got %d", len(rows))
		}
		if rows[1][1] != "git log" || rows[2][1] != "go test" || rows[3][1] != "go test" {
			t.Errorf("order wrong: %v", rows)
		}
		if rows[1][2] != "relaxed" || rows[1][3] != "500" || rows[1][4] != "250" || rows[1][6] != "50.0" {
			t.Errorf("fields wrong: %v", rows[1])
		}
	})
	t.Run("default limit is 20", func(t *testing.T) {
		big := newTestStore(t)
		var runs []stats.Run
		for i := 0; i < 25; i++ {
			runs = append(runs, stats.Run{ID: string(rune('a'+i)) + "x", At: at(0, 0).Add(time.Duration(i) * time.Minute),
				Command: "go test", Tier: "aggressive", RawTokens: 10, OutTokens: 5, SavedTokens: 5})
		}
		seed(t, big, runs)
		if rows := csvRows(t, renderViews(t, big, Options{History: true, Format: "csv"})); len(rows) != 21 {
			t.Errorf("got %d rows, want header+20", len(rows))
		}
	})
	t.Run("text columns and truncation", func(t *testing.T) {
		s := newTestStore(t)
		seed(t, s, []stats.Run{{ID: "z", At: at(0, 9), Command: strings.Repeat("c", 40), Tier: "aggressive",
			RawTokens: 1000, OutTokens: 250, SavedTokens: 750}})
		out := renderViews(t, s, Options{History: true})
		for _, w := range []string{"Tier", "Raw → Out", "1.0K → 250", "75.0%", "cccccccccccccccccccc..."} {
			if !strings.Contains(out, w) {
				t.Errorf("missing %q in:\n%s", w, out)
			}
		}
	})
}

func TestSummaryCSV(t *testing.T) {
	store := seedViews(t)
	rows := csvRows(t, renderViews(t, store, Options{Format: "csv"}))
	if got := strings.Join(rows[0], ","); got != "command,runs,raw_tokens,out_tokens,saved_tokens,saved_pct,avg_ms" {
		t.Fatalf("header = %s", got)
	}
	if rows[1][0] != "(total)" || rows[1][1] != "5" || rows[1][2] != "4600" {
		t.Errorf("total row = %v", rows[1])
	}
	if len(rows) != 1+1+4 {
		t.Errorf("want header+total+4 commands, got %d rows", len(rows))
	}
}
