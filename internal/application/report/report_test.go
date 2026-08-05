package report

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/domain/stats"
)

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
	if !strings.Contains(out, "Total commands:  1") {
		t.Fatalf("missing total commands line, got:\n%s", out)
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
	if !strings.Contains(out, "Project Scope:   org/repo-a") {
		t.Fatalf("missing project scope header, got:\n%s", out)
	}
	if !strings.Contains(out, "Total commands:  1") {
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
