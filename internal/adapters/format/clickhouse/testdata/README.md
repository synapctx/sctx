Captured with `clickhouse-client` from `clickhouse/clickhouse-server:25.3`
against that server on a user-defined docker network (table `app.orders`, 2000
rows). stdout is not a terminal, so the default format is TSV. `pc230` =
`--format PrettyCompact` 230 rows, `p230` = `--format Pretty`, `tsv300` = the
default, `vert` = Vertical, `csv` = CSVWithNames (declined shapes). Failures
(`syntax` 62, `missing` 60, `auth` 36, `refused` 210) have the real exit status
in `*.exit`; the `auth` capture is an argument error from reusing `--password`.
Hostnames were rewritten to `db`.
