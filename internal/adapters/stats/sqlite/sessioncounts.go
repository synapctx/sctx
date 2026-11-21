package sqlite

import (
	"context"
	"fmt"
	"time"
)

// SessionRunCounts counts recorded runs per (agent session id, command key)
// since the given instant (zero = all time), ignoring runs with no session id.
// It backs `sctx discover`, which uses it to tell a command the hook already
// wrapped from one that really ran bare: a transcript records the command as
// the model wrote it, before any hook rewrote it. Local read only.
func (s *Store) SessionRunCounts(ctx context.Context, since time.Time) (map[string]map[string]int64, error) {
	query := `SELECT session_id, command, COUNT(*) FROM runs WHERE session_id <> ''`
	var args []any
	if !since.IsZero() {
		query += ` AND at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	query += ` GROUP BY session_id, command`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying session run counts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]map[string]int64)
	for rows.Next() {
		var sid, command string
		var n int64
		if err := rows.Scan(&sid, &command, &n); err != nil {
			return nil, fmt.Errorf("scanning session run counts: %w", err)
		}
		if out[sid] == nil {
			out[sid] = make(map[string]int64)
		}
		out[sid][command] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session run counts: %w", err)
	}
	return out, nil
}
