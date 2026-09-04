// Package sqlite persists run accounting in a single local SQLite database
// (pure-Go driver, WAL mode) so concurrent sctx invocations can record safely
// and `sctx gain` can aggregate cheaply.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/synapctx/sctx/internal/domain/stats"
)

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	at TEXT NOT NULL,
	command TEXT NOT NULL,
	argv TEXT NOT NULL,
	formatter TEXT NOT NULL,
	tier TEXT NOT NULL,
	raw_bytes INTEGER NOT NULL,
	raw_tokens INTEGER NOT NULL,
	out_tokens INTEGER NOT NULL,
	saved_tokens INTEGER NOT NULL,
	exit_code INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	anomaly TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_command ON runs(command);
`

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating stats directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening stats db: %w", err)
	}
	// ONE CONNECTION, because SQLite allows one writer and database/sql will
	// happily open several.
	//
	// WAL and busy_timeout make CONCURRENT PROCESSES safe — the case this store
	// was built for, several sctx invocations recording at once — but they do not
	// help a single process competing with itself: goroutines here take separate
	// pooled connections, and the loser gets SQLITE_BUSY rather than waiting its
	// turn in the same connection's queue. Seen as an intermittent
	// "database is locked (5)" under CI load on 2026-08-18, on a test that had
	// passed on every previous run and passes locally.
	//
	// The cost is that in-process writes serialise, which is what SQLite does
	// anyway. The alternative — a longer timeout — makes the window smaller
	// without closing it. The timeout above still matters for the cross-process
	// case, and is raised to match a loaded machine.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing stats schema: %w", err)
	}
	// Idempotent migration: the "repository" column was added after the
	// initial schema, so CREATE TABLE IF NOT EXISTS won't add it to
	// existing databases. Older rows keep repository = '' (unscoped).
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN repository TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrating stats schema: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_repository ON runs(repository)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing stats schema: %w", err)
	}
	// Same idempotent-migration idiom as repository above: client/session/argv
	// fingerprint/bypass provenance and a reserved redaction count, added after
	// the initial schema. Older rows keep the zero value for each.
	for _, stmt := range []string{
		`ALTER TABLE runs ADD COLUMN client TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN argv_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN bypass TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN redacted_count INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
			db.Close()
			return nil, fmt.Errorf("migrating stats schema: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_client ON runs(client)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing stats schema: %w", err)
	}
	return &Store{db: db}, nil
}

// isDuplicateColumn reports whether err is SQLite's "column already exists"
// error, the expected outcome when the repository-column migration runs
// against a database that already has it.
func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column")
}

func (s *Store) Record(ctx context.Context, r stats.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, at, command, argv, formatter, tier, raw_bytes, raw_tokens, out_tokens, saved_tokens, exit_code, duration_ms, anomaly, repository, client, session_id, argv_hash, bypass, redacted_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.At.Format(time.RFC3339Nano), r.Command, r.Argv, r.Formatter, r.Tier,
		r.RawBytes, r.RawTokens, r.OutTokens, r.SavedTokens, r.ExitCode, r.DurationMS, r.Anomaly, r.Repository,
		r.Client, r.SessionID, r.ArgvHash, r.Bypass, r.RedactedCount)
	if err != nil {
		return fmt.Errorf("recording run: %w", err)
	}
	return nil
}

func (s *Store) Aggregate(ctx context.Context, opts stats.AggregateOptions) (stats.Report, error) {
	var report stats.Report

	where, args := whereClause(opts)

	var since sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(raw_tokens),0), COALESCE(SUM(out_tokens),0),
		        COALESCE(SUM(saved_tokens),0), COALESCE(SUM(duration_ms),0), MIN(at),
		        COALESCE(SUM(redacted_count),0)
		 FROM runs`+where, args...)
	if err := row.Scan(&report.Global.Runs, &report.Global.RawTokens, &report.Global.OutTokens,
		&report.Global.SavedTokens, &report.TotalExecMS, &since, &report.RedactedCount); err != nil {
		return stats.Report{}, fmt.Errorf("aggregating global stats: %w", err)
	}
	if report.Global.Runs > 0 {
		report.Global.AvgMS = report.TotalExecMS / report.Global.Runs
	}
	if since.Valid {
		if t, err := time.Parse(time.RFC3339Nano, since.String); err == nil {
			report.Since = t
		}
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT command, COUNT(*), COALESCE(SUM(raw_tokens),0), COALESCE(SUM(out_tokens),0),
		        COALESCE(SUM(saved_tokens),0), COALESCE(SUM(duration_ms),0)
		 FROM runs`+where+` GROUP BY command ORDER BY SUM(saved_tokens) DESC`, args...)
	if err != nil {
		return stats.Report{}, fmt.Errorf("aggregating per-command stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ct stats.CommandTotals
		var totalMS int64
		if err := rows.Scan(&ct.Command, &ct.Runs, &ct.RawTokens, &ct.OutTokens, &ct.SavedTokens, &totalMS); err != nil {
			return stats.Report{}, fmt.Errorf("scanning per-command stats: %w", err)
		}
		if ct.Runs > 0 {
			ct.AvgMS = totalMS / ct.Runs
		}
		report.ByCommand = append(report.ByCommand, ct)
	}
	if err := rows.Err(); err != nil {
		return stats.Report{}, fmt.Errorf("iterating per-command stats: %w", err)
	}
	return report, nil
}

// Failures returns the most recent degraded runs (tier "verbatim" or a
// non-empty anomaly) matching opts, newest first, capped at limit.
func (s *Store) Failures(ctx context.Context, opts stats.AggregateOptions, limit int) ([]stats.FailedRun, error) {
	if limit <= 0 {
		limit = 20
	}
	where, args := whereClause(opts)
	const failCond = "(tier = 'verbatim' OR anomaly != '')"
	if where == "" {
		where = " WHERE " + failCond
	} else {
		where += " AND " + failCond
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx,
		`SELECT command, argv, tier, anomaly, at FROM runs`+where+` ORDER BY at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying failures: %w", err)
	}
	defer rows.Close()

	var out []stats.FailedRun
	for rows.Next() {
		var fr stats.FailedRun
		var atStr string
		if err := rows.Scan(&fr.Command, &fr.Argv, &fr.Tier, &fr.Anomaly, &atStr); err != nil {
			return nil, fmt.Errorf("scanning failures: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
			fr.At = t
		}
		out = append(out, fr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating failures: %w", err)
	}
	return out, nil
}

// ByClient aggregates runs matching opts by which coding agent ran them.
func (s *Store) ByClient(ctx context.Context, opts stats.AggregateOptions) ([]stats.ClientTotals, error) {
	where, args := whereClause(opts)
	rows, err := s.db.QueryContext(ctx,
		`SELECT client, COUNT(*), COALESCE(SUM(raw_tokens),0), COALESCE(SUM(out_tokens),0),
		        COALESCE(SUM(saved_tokens),0), COALESCE(SUM(duration_ms),0)
		 FROM runs`+where+` GROUP BY client ORDER BY SUM(saved_tokens) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating by client: %w", err)
	}
	defer rows.Close()

	var out []stats.ClientTotals
	for rows.Next() {
		var ct stats.ClientTotals
		var totalMS int64
		if err := rows.Scan(&ct.Client, &ct.Runs, &ct.RawTokens, &ct.OutTokens, &ct.SavedTokens, &totalMS); err != nil {
			return nil, fmt.Errorf("scanning by-client stats: %w", err)
		}
		if ct.Runs > 0 {
			ct.AvgMS = totalMS / ct.Runs
		}
		out = append(out, ct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating by-client stats: %w", err)
	}
	return out, nil
}

// RepeatedRunsToday reports argv values run more than once since local
// midnight, heaviest first. Grouped on the plain argv column — never
// ArgvHash — because this reads the LOCAL store for a LOCAL report; nothing
// here leaves the machine.
func (s *Store) RepeatedRunsToday(ctx context.Context, limit int) ([]stats.RepeatedRun, error) {
	if limit <= 0 {
		limit = 5
	}
	startOfDay := time.Now().Local().Truncate(24 * time.Hour)
	rows, err := s.db.QueryContext(ctx,
		`SELECT argv, COUNT(*) AS n FROM runs WHERE at >= ? GROUP BY argv HAVING n > 1 ORDER BY n DESC LIMIT ?`,
		startOfDay.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("querying repeated runs: %w", err)
	}
	defer rows.Close()

	var out []stats.RepeatedRun
	for rows.Next() {
		var rr stats.RepeatedRun
		if err := rows.Scan(&rr.Argv, &rr.Count); err != nil {
			return nil, fmt.Errorf("scanning repeated runs: %w", err)
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repeated runs: %w", err)
	}
	return out, nil
}

// whereClause builds the optional WHERE clause and its bound args for opts.
// An empty Repository or zero Since means "no filter on that dimension";
// a fully empty opts yields ("", nil).
func whereClause(opts stats.AggregateOptions) (string, []any) {
	var clauses []string
	var args []any
	if opts.Repository != "" {
		clauses = append(clauses, "repository = ?")
		args = append(args, opts.Repository)
	}
	if !opts.Since.IsZero() {
		clauses = append(clauses, "at >= ?")
		args = append(args, opts.Since.UTC().Format(time.RFC3339Nano))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *Store) Close() error {
	return s.db.Close()
}
