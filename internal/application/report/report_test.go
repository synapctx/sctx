package report

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/domain/stats"
)

// ansiEscape matches a single ANSI SGR escape sequence, e.g. "\x1b[1;32m" or
// the reset "\x1b[0m". Used only by tests, to check the PLAIN width of a
// colored line.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seed(t *testing.T, store *sqlite.Store, runs []stats.Run) {
	t.Helper()
	ctx := context.Background()
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}
}

func TestRenderDefaultTextUnscoped(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Project Scope") || strings.Contains(out, "Window:") {
		t.Fatalf("unscoped report must not print scope lines, got:\n%s", out)
	}
	if !strings.HasPrefix(out, "sctx Token Savings\n") {
		t.Fatalf("unexpected header, got:\n%s", out)
	}
	if !strings.Contains(out, "Total commands:   1") {
		t.Fatalf("missing total commands line, got:\n%s", out)
	}
}

// TestRenderReportsSecretsKeptOutOfContext covers `sctx gain`'s redaction
// line: it sums RedactedCount across the scoped runs and prints nothing when
// the total is zero (the common case today, since redaction is opt-in).
func TestRenderReportsSecretsKeptOutOfContext(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900, RedactedCount: 2},
		{ID: "01B", At: time.Now().UTC(), Command: "curl", Argv: "curl example", Tier: "verbatim", RawTokens: 100, OutTokens: 100, SavedTokens: 0, RedactedCount: 1},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "Secrets redacted: 3 (never reached the model)") {
		t.Fatalf("missing redaction summary line, got:\n%s", buf.String())
	}

	var jsonBuf strings.Builder
	if err := Render(context.Background(), store, &jsonBuf, Options{Format: "json"}); err != nil {
		t.Fatalf("Render json: %v", err)
	}
	var decoded struct {
		RedactedCount int64 `json:"redacted_count"`
	}
	if err := json.Unmarshal([]byte(jsonBuf.String()), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.RedactedCount != 3 {
		t.Fatalf("RedactedCount = %d, want 3", decoded.RedactedCount)
	}
}

// TestRenderOmitsRedactionLineWhenZero keeps the default (no redaction
// configured) report unchanged from before this feature existed.
func TestRenderOmitsRedactionLineWhenZero(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(buf.String(), "Secrets redacted:") {
		t.Fatalf("redaction line should be omitted at zero, got:\n%s", buf.String())
	}
}

func TestRenderColorAndPlain(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900},
	})

	// Plain (default) output carries no ANSI escapes.
	var plain strings.Builder
	if err := Render(context.Background(), store, &plain, Options{}); err != nil {
		t.Fatalf("Render plain: %v", err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain report must not contain ANSI escapes, got:\n%q", plain.String())
	}

	// With Color, escapes appear but the plain content is still present
	// (color wraps content, never replaces it).
	var colored strings.Builder
	if err := Render(context.Background(), store, &colored, Options{Color: true}); err != nil {
		t.Fatalf("Render color: %v", err)
	}
	out := colored.String()
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("colored report must contain ANSI escapes, got:\n%q", out)
	}
	if !strings.Contains(out, "sctx Token Savings") || !strings.Contains(out, "go test") {
		t.Fatalf("colored report lost its content, got:\n%q", out)
	}
}

func TestRenderProjectScopedHeader(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900, Repository: "org/repo-a"},
		{ID: "01B", At: time.Now().UTC(), Command: "go test", Argv: "go test ./x", Tier: "aggressive", RawTokens: 500, OutTokens: 50, SavedTokens: 450, Repository: "org/repo-b"},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Repository: "org/repo-a"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Project Scope:    org/repo-a") {
		t.Fatalf("missing project scope header, got:\n%s", out)
	}
	if !strings.Contains(out, "Total commands:   1") {
		t.Fatalf("project scope should only count repo-a's run, got:\n%s", out)
	}
}

func TestRenderFailuresText(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seed(t, store, []stats.Run{
		{ID: "01A", At: now, Command: "go test", Argv: "go test ./...", Tier: "aggressive"},
		{ID: "01B", At: now.Add(time.Second), Command: "curl", Argv: "curl https://x", Tier: "verbatim"},
		{ID: "01C", At: now.Add(2 * time.Second), Command: "git status", Argv: "git status", Tier: "relaxed", Anomaly: "empty render"},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Failures: true}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "sctx Degradation Log\n") {
		t.Fatalf("unexpected header, got:\n%s", out)
	}
	if !strings.Contains(out, "curl https://x") || !strings.Contains(out, "verbatim") {
		t.Fatalf("missing verbatim failure row, got:\n%s", out)
	}
	if !strings.Contains(out, "git status") || !strings.Contains(out, "empty render") {
		t.Fatalf("missing anomaly failure row, got:\n%s", out)
	}
	if strings.Contains(out, "go test ./...") {
		t.Fatalf("clean run must not appear in failures log, got:\n%s", out)
	}
}

func TestRenderFailuresTextEmpty(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Tier: "aggressive"},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Failures: true}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "No degraded runs recorded.") {
		t.Fatalf("expected empty-failures message, got:\n%s", buf.String())
	}
}

func TestRenderJSONReport(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test ./...", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900, Repository: "org/repo-a"},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Repository: "org/repo-a", Format: "json"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if out.Scope != "project" || out.Repository != "org/repo-a" {
		t.Fatalf("scope/repository = %q/%q, want project/org/repo-a", out.Scope, out.Repository)
	}
	if out.Global.Runs != 1 || out.Global.SavedTokens != 900 {
		t.Fatalf("global = %+v, want 1 run / 900 saved", out.Global)
	}
	if len(out.ByCommand) != 1 || out.ByCommand[0].Command != "go test" {
		t.Fatalf("by_command = %+v, want single go test entry", out.ByCommand)
	}
}

func TestRenderJSONFailures(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "curl", Argv: "curl https://x", Tier: "verbatim"},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Failures: true, Format: "json"}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var out jsonFailures
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if out.Scope != "global" {
		t.Fatalf("scope = %q, want global", out.Scope)
	}
	if len(out.Failures) != 1 || out.Failures[0].Command != "curl https://x" || out.Failures[0].Tier != "verbatim" {
		t.Fatalf("failures = %+v, want single curl verbatim entry", out.Failures)
	}
}

// TestRenderSharePlainOmitsArgvAndPaths feeds the store rows whose argv
// carries a path and a secret-shaped token (never rendered by the report
// itself), and asserts the --share card contains only aggregate numbers and
// the (already path/argv-free) normalized command key.
func TestRenderSharePlainOmitsArgvAndPaths(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "go test", Argv: "go test /Users/sebastian/secret-project/./... -run TestX --token=sctx_live_abcdef123456", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900},
		{ID: "01B", At: time.Now().UTC(), Command: "git status", Argv: "git -C /Users/sebastian/.ssh status", Tier: "aggressive", RawTokens: 200, OutTokens: 20, SavedTokens: 180},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Share: true, Version: "1.2.3"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	forbidden := []string{"/Users/", "/.ssh", "sctx_live_", "-run TestX", "--token", "secret-project"}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("share card leaked %q, got:\n%s", f, out)
		}
	}
	for _, want := range []string{"go test", "git status", "tokens = bytes/4, a floor", "sctx 1.2.3", "all-time"} {
		if !strings.Contains(out, want) {
			t.Errorf("share card missing %q, got:\n%s", want, out)
		}
	}
}

// TestRenderShareMarkdownOmitsArgvAndPaths is the markdown-variant twin of
// the plain-text leak guard above.
func TestRenderShareMarkdownOmitsArgvAndPaths(t *testing.T) {
	store := newTestStore(t)
	seed(t, store, []stats.Run{
		{ID: "01A", At: time.Now().UTC(), Command: "npm install", Argv: "npm install --registry https://user:hunter2@registry.example.com/ /home/dev/app", Tier: "relaxed", RawTokens: 500, OutTokens: 50, SavedTokens: 450},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{Share: true, Format: "markdown", Version: "9.9.9"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	forbidden := []string{"/home/dev", "hunter2", "registry.example.com", "user:hunter2"}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("markdown share card leaked %q, got:\n%s", f, out)
		}
	}
	if !strings.HasPrefix(out, "**sctx token savings") {
		t.Fatalf("markdown share card must open with a bold heading, got:\n%s", out)
	}
	if !strings.Contains(out, "`npm install`") {
		t.Errorf("markdown share card missing the program row, got:\n%s", out)
	}
	if !strings.Contains(out, "sctx 9.9.9") {
		t.Errorf("markdown share card missing the version, got:\n%s", out)
	}
}

// TestReportNoLineExceedsTableWidth is the width regression guard for the
// bug this change fixes: every line the (text) report emits, ANSI stripped,
// must be at most tableWidth wide. The Repeated Identical Runs section used
// to print one unbounded "Repeated identical runs today: N (top: ...)" line
// that wrapped in a normal terminal; this must never come back silently.
func TestReportNoLineExceedsTableWidth(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	var runs []stats.Run
	id := 0
	nextID := func() string {
		id++
		return fmt.Sprintf("%04d", id)
	}
	for _, cmd := range []string{"go build ./...", "gofmt -l .", "go vet ./...", "git status --short", "go test -count=1 ./..."} {
		for i := 0; i < 3; i++ {
			runs = append(runs, stats.Run{ID: nextID(), At: now, Command: cmd, Argv: cmd, Tier: "aggressive", RawTokens: 100, OutTokens: 10, SavedTokens: 90})
		}
	}
	seed(t, store, runs)

	for _, colored := range []bool{false, true} {
		var buf strings.Builder
		if err := Render(context.Background(), store, &buf, Options{Color: colored, ByClient: true}); err != nil {
			t.Fatalf("Render(color=%v): %v", colored, err)
		}
		for i, line := range strings.Split(buf.String(), "\n") {
			plain := stripANSI(line)
			if n := utf8.RuneCountInString(plain); n > tableWidth {
				t.Fatalf("line %d exceeds tableWidth (%d): %q (len %d)", i, tableWidth, plain, n)
			}
		}
	}
}

// TestRenderRepeatedRunsTodayTrueTotals proves the footer reports the TRUE
// totals from the store (RepeatedRunsTodaySummary), not the sum of the rows
// actually shown — the bug this change fixes reported the sum of the
// (at most 5) shown rows as if it were the whole day's total.
func TestRenderRepeatedRunsTodayTrueTotals(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	var runs []stats.Run
	id := 0
	nextID := func() string {
		id++
		return fmt.Sprintf("%04d", id)
	}
	commands := []struct {
		argv string
		n    int
	}{
		{"go build ./...", 66},
		{"gofmt -l .", 52},
		{"go vet ./...", 50},
		{"git status --short", 23},
		{"go test -count=1 ./...", 21},
		{"golangci-lint run", 27},
	}
	total := 0
	for _, c := range commands {
		total += c.n
		for i := 0; i < c.n; i++ {
			runs = append(runs, stats.Run{ID: nextID(), At: now, Argv: c.argv})
		}
	}
	seed(t, store, runs)

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Repeated Identical Runs") {
		t.Fatalf("missing section title, got:\n%s", out)
	}
	wantFooter := fmt.Sprintf("%d identical runs today across %d commands · top 5 shown", total, len(commands))
	if !strings.Contains(out, wantFooter) {
		t.Fatalf("footer missing true totals + shown clause, want %q, got:\n%s", wantFooter, out)
	}
	// The shown rows sum to less than the true total: proves the number in
	// the footer is not merely the sum of what's rendered.
	shownSum := 66 + 52 + 50 + 23 + 27 // top 5 by count, descending
	if shownSum == total {
		t.Fatalf("test fixture degenerate: shown sum equals true total")
	}
}

// TestRenderRepeatedRunsTodayOmitsShownClauseWhenComplete covers the
// complementary case: when every repeated command fits in the shown rows,
// the footer must not print a redundant "top N shown" clause.
func TestRenderRepeatedRunsTodayOmitsShownClauseWhenComplete(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seed(t, store, []stats.Run{
		{ID: "0001", At: now, Argv: "go test ./..."},
		{ID: "0002", At: now, Argv: "go test ./..."},
	})

	var buf strings.Builder
	if err := Render(context.Background(), store, &buf, Options{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 identical runs today across 1 command\n") {
		t.Fatalf("expected singular-command footer with no shown-clause, got:\n%s", out)
	}
	if strings.Contains(out, "shown") {
		t.Fatalf("footer should omit the shown-clause when every command is displayed, got:\n%s", out)
	}
}

// TestRenderRepeatedRunsTodaySingularWording drives renderRepeatedRunsToday
// directly (rather than through a seeded store) to hit the singular-count
// edge cases: 1 run and 1 command are grammatically impossible together from
// a real store (a repeated group needs at least 2 runs), but the renderer's
// pluralization must still be correct in isolation.
func TestRenderRepeatedRunsTodaySingularWording(t *testing.T) {
	p := palette{on: false}
	var buf strings.Builder
	renderRepeatedRunsToday(&buf, p, []stats.RepeatedRun{{Argv: "go test ./...", Count: 1}}, 1, 1)
	out := buf.String()
	if !strings.Contains(out, "1 identical run today across 1 command\n") {
		t.Fatalf("expected singular run+command wording, got:\n%s", out)
	}
	if strings.Contains(out, "shown") {
		t.Fatalf("footer should omit the shown-clause when nothing is truncated, got:\n%s", out)
	}
}

// TestRenderShareTop5AndScope exercises the 5-row cap and the --project/
// --since scope lines flowing through to the window label.
func TestRenderShareTop5AndScope(t *testing.T) {
	store := newTestStore(t)
	runs := make([]stats.Run, 0, 6)
	for i, cmd := range []string{"go test", "go vet", "git status", "npm install", "pytest", "make build"} {
		runs = append(runs, stats.Run{
			ID: "01" + string(rune('A'+i)), At: time.Now().UTC(), Command: cmd, Argv: cmd,
			Tier: "aggressive", RawTokens: int64(100 * (6 - i)), OutTokens: 10, SavedTokens: int64(90 * (6 - i)),
		})
	}
	seed(t, store, runs)

	var buf strings.Builder
	since := time.Now().Add(-7 * 24 * time.Hour)
	if err := Render(context.Background(), store, &buf, Options{Share: true, Since: since}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "make build") {
		t.Errorf("share card must cap at 5 programs, got a 6th:\n%s", out)
	}
	if !strings.Contains(out, "last 7d") {
		t.Errorf("share card window must reflect --since, got:\n%s", out)
	}
}
