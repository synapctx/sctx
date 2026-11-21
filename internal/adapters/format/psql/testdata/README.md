# Native psql fixtures

Captured on 2026-08-15 with the macOS `psql` 18.4 client against the official
`postgres:16-alpine` container (PostgreSQL 16.15). Commands used `-X` so user
startup files could not alter output. Both fixtures exited 0 with empty stderr.

- `aligned.stdout`: `psql -X ... -P null='∅' -c "SELECT id, name, status, note FROM jobs ORDER BY id;"`
- `expanded.stdout`: the same query with `-x` and `LIMIT 12`

The table includes a SQL NULL rendered as `∅` and a Unicode value. The fixtures
are intentionally native output, not verbose output requested to manufacture a
larger savings claim.

## postgres:17 captures (2026-11)

`pg17_*` were captured with the `postgres:17` client against a `postgres:17`
server over a user-defined docker network (customers/orders tables, 2k rows).
`rows400` = aligned SELECT of 400 rows, `records120` = `-x` of 120 records,
`tuples300` = `-t -A`, `csv300` = `--csv`, `timing` = `\timing on` footer,
`syntax` = stderr of a failing statement (exit 1).
