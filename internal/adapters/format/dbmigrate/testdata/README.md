`flyway11_*`: flyway/flyway:11 (Flyway OSS 11.20.3) against postgres:17 on a
user-defined docker network (3 SQL migrations; `migrate_fail` adds a migration
against a missing table, `validate_fail` edits an applied migration).
`liquibase431_*`: liquibase/liquibase:4.31 (4.31.1), 3 XML changesets;
`updatefail` runs a changeset whose SQL fails. `.out`/`.err` are the container's
stdout/stderr, `.exit` the real status. Hostnames were rewritten to `db`.
