package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/stats"
)

func TestRecordAndAggregate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	runs := []stats.Run{
		{ID: "01A", At: now, Command: "go test", Argv: "go test ./...", Formatter: "go", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900, DurationMS: 1500},
		{ID: "01B", At: now, Command: "go test", Argv: "go test ./x", Formatter: "go", Tier: "aggressive", RawTokens: 500, OutTokens: 50, SavedTokens: 450, DurationMS: 500},
		{ID: "01C", At: now, Command: "git status", Argv: "git status", Formatter: "git", Tier: "relaxed", RawTokens: 200, OutTokens: 100, SavedTokens: 100, DurationMS: 20},
		{ID: "01D", At: now, Command: "ls", Argv: "ls", Formatter: "verbatim", Tier: "verbatim", RawTokens: 40, OutTokens: 40, SavedTokens: 0, DurationMS: 5},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}

	rep, err := store.Aggregate(ctx, stats.AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if rep.Global.Runs != 4 {
		t.Fatalf("runs = %d, want 4", rep.Global.Runs)
	}
	if rep.Global.SavedTokens != 1450 {
		t.Fatalf("saved = %d, want 1450", rep.Global.SavedTokens)
	}
	if rep.TotalExecMS != 2025 {
		t.Fatalf("total exec ms = %d, want 2025", rep.TotalExecMS)
	}
	if len(rep.ByCommand) != 3 {
		t.Fatalf("by-command groups = %d, want 3", len(rep.ByCommand))
	}
	// Ordered by saved tokens descending.
	if rep.ByCommand[0].Command != "go test" || rep.ByCommand[0].SavedTokens != 1350 {
		t.Fatalf("top command = %+v, want go test with 1350 saved", rep.ByCommand[0])
	}
	if rep.ByCommand[0].Runs != 2 {
		t.Fatalf("go test runs = %d, want 2", rep.ByCommand[0].Runs)
	}
}

func TestConcurrentRecords(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	done := make(chan error, 20)
	for i := range 20 {
		go func(i int) {
			done <- store.Record(ctx, stats.Run{
				ID: string(rune('A'+i)) + "-run", At: time.Now().UTC(),
				Command: "go test", Argv: "go test", Formatter: "go", Tier: "aggressive",
			})
		}(i)
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Record: %v", err)
		}
	}

	rep, err := store.Aggregate(ctx, stats.AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if rep.Global.Runs != 20 {
		t.Fatalf("runs = %d, want 20", rep.Global.Runs)
	}
}

func TestAggregateRepositoryAndSinceFilter(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	runs := []stats.Run{
		{ID: "01A", At: now, Command: "go test", Argv: "go test ./...", Formatter: "go", Tier: "aggressive", RawTokens: 1000, OutTokens: 100, SavedTokens: 900, Repository: "org/repo-a"},
		{ID: "01B", At: now, Command: "go test", Argv: "go test ./x", Formatter: "go", Tier: "aggressive", RawTokens: 500, OutTokens: 50, SavedTokens: 450, Repository: "org/repo-b"},
		{ID: "01C", At: old, Command: "go test", Argv: "go test ./old", Formatter: "go", Tier: "aggressive", RawTokens: 200, OutTokens: 20, SavedTokens: 180, Repository: "org/repo-a"},
		{ID: "01D", At: now, Command: "ls", Argv: "ls", Formatter: "verbatim", Tier: "verbatim", RawTokens: 40, OutTokens: 40, SavedTokens: 0, Repository: ""},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}

	// Repository filter.
	rep, err := store.Aggregate(ctx, stats.AggregateOptions{Repository: "org/repo-a"})
	if err != nil {
		t.Fatalf("Aggregate(repo-a): %v", err)
	}
	if rep.Global.Runs != 2 {
		t.Fatalf("repo-a runs = %d, want 2", rep.Global.Runs)
	}
	if rep.Global.SavedTokens != 1080 {
		t.Fatalf("repo-a saved = %d, want 1080", rep.Global.SavedTokens)
	}

	// Since filter excludes the old run even without a repository filter.
	rep, err = store.Aggregate(ctx, stats.AggregateOptions{Since: now.Add(-1 * time.Hour)})
	if err != nil {
		t.Fatalf("Aggregate(since): %v", err)
	}
	if rep.Global.Runs != 3 {
		t.Fatalf("since runs = %d, want 3", rep.Global.Runs)
	}

	// Combined repository + since filter.
	rep, err = store.Aggregate(ctx, stats.AggregateOptions{Repository: "org/repo-a", Since: now.Add(-1 * time.Hour)})
	if err != nil {
		t.Fatalf("Aggregate(repo-a+since): %v", err)
	}
	if rep.Global.Runs != 1 {
		t.Fatalf("repo-a+since runs = %d, want 1", rep.Global.Runs)
	}
	if rep.Global.SavedTokens != 900 {
		t.Fatalf("repo-a+since saved = %d, want 900", rep.Global.SavedTokens)
	}
}

func TestRepositoryColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.db")

	// Simulate a pre-migration database: open it directly with the legacy
	// schema (no repository column) before NewStore ever runs.
	legacyDB, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)")
	if err != nil {
		t.Fatalf("opening legacy db: %v", err)
	}
	if _, err := legacyDB.Exec(schema); err != nil {
		t.Fatalf("creating legacy schema: %v", err)
	}
	if _, err := legacyDB.Exec(
		`INSERT INTO runs (id, at, command, argv, formatter, tier, raw_bytes, raw_tokens, out_tokens, saved_tokens, exit_code, duration_ms, anomaly)
		 VALUES ('01X', ?, 'go test', 'go test', 'go', 'aggressive', 0, 100, 10, 90, 0, 0, '')`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("inserting legacy row: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("closing legacy db: %v", err)
	}

	// Opening via NewStore must migrate in place, without erroring, and the
	// pre-existing row must read back with repository = "".
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore (migration): %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	rep, err := store.Aggregate(ctx, stats.AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate after migration: %v", err)
	}
	if rep.Global.Runs != 1 {
		t.Fatalf("runs after migration = %d, want 1", rep.Global.Runs)
	}
	rep, err = store.Aggregate(ctx, stats.AggregateOptions{Repository: "org/repo"})
	if err != nil {
		t.Fatalf("Aggregate scoped after migration: %v", err)
	}
	if rep.Global.Runs != 0 {
		t.Fatalf("scoped runs after migration = %d, want 0 (legacy row has repository='')", rep.Global.Runs)
	}

	// Reopening an already-migrated db must be a no-op, not an error.
	store2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore (re-migration): %v", err)
	}
	defer store2.Close()
}

func TestFailures(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	runs := []stats.Run{
		{ID: "01A", At: now, Command: "go test", Argv: "go test ./...", Tier: "aggressive", Anomaly: "", Repository: "org/repo-a"},
		{ID: "01B", At: now.Add(time.Second), Command: "curl", Argv: "curl https://example.com", Tier: "verbatim", Anomaly: "", Repository: "org/repo-a"},
		{ID: "01C", At: now.Add(2 * time.Second), Command: "git status", Argv: "git status", Tier: "relaxed", Anomaly: "empty render", Repository: "org/repo-b"},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}

	fails, err := store.Failures(ctx, stats.AggregateOptions{}, 10)
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	if len(fails) != 2 {
		t.Fatalf("failures = %d, want 2", len(fails))
	}
	// Newest first.
	if fails[0].Command != "git status" || fails[0].Anomaly != "empty render" {
		t.Fatalf("fails[0] = %+v, want git status with anomaly", fails[0])
	}
	if fails[1].Command != "curl" || fails[1].Tier != "verbatim" {
		t.Fatalf("fails[1] = %+v, want curl verbatim", fails[1])
	}

	scoped, err := store.Failures(ctx, stats.AggregateOptions{Repository: "org/repo-a"}, 10)
	if err != nil {
		t.Fatalf("Failures(scoped): %v", err)
	}
	if len(scoped) != 1 || scoped[0].Command != "curl" {
		t.Fatalf("scoped failures = %+v, want just curl", scoped)
	}
}

func TestByClient(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	runs := []stats.Run{
		{ID: "01A", At: now, Command: "go test", Argv: "go test ./...", Client: "claude-code", RawTokens: 1000, OutTokens: 100, SavedTokens: 900},
		{ID: "01B", At: now, Command: "go vet", Argv: "go vet ./...", Client: "claude-code", RawTokens: 500, OutTokens: 50, SavedTokens: 450},
		{ID: "01C", At: now, Command: "ls", Argv: "ls", Client: "shell", RawTokens: 40, OutTokens: 40, SavedTokens: 0},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}

	byClient, err := store.ByClient(ctx, stats.AggregateOptions{})
	if err != nil {
		t.Fatalf("ByClient: %v", err)
	}
	if len(byClient) != 2 {
		t.Fatalf("got %d client groups, want 2: %+v", len(byClient), byClient)
	}
	if byClient[0].Client != "claude-code" || byClient[0].Runs != 2 || byClient[0].SavedTokens != 1350 {
		t.Fatalf("top client group = %+v, want claude-code/2/1350", byClient[0])
	}
	if byClient[1].Client != "shell" || byClient[1].Runs != 1 {
		t.Fatalf("second client group = %+v, want shell/1", byClient[1])
	}
}

func TestRepeatedRunsToday(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	yesterday := now.Add(-25 * time.Hour)
	runs := []stats.Run{
		{ID: "01A", At: now, Argv: "go test ./..."},
		{ID: "01B", At: now, Argv: "go test ./..."},
		{ID: "01C", At: now, Argv: "go test ./..."},
		{ID: "01D", At: now, Argv: "git status"},
		// Not repeated (only once today).
		{ID: "01E", At: now, Argv: "go build ./..."},
		// Repeated, but yesterday — must not count toward today's total.
		{ID: "01F", At: yesterday, Argv: "ls -la"},
		{ID: "01G", At: yesterday, Argv: "ls -la"},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r); err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
	}

	repeated, err := store.RepeatedRunsToday(ctx, 0)
	if err != nil {
		t.Fatalf("RepeatedRunsToday: %v", err)
	}
	if len(repeated) != 1 {
		t.Fatalf("got %d repeated groups, want 1 (only today's `go test ./...`): %+v", len(repeated), repeated)
	}
	if repeated[0].Argv != "go test ./..." || repeated[0].Count != 3 {
		t.Fatalf("got %+v, want go test ./... x3", repeated[0])
	}
}
